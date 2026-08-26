package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/trtc"
)

type Options struct {
	TRTCIssuer     trtc.CredentialIssuer
	TRTCDebugToken string
	TRTCAuthorizer trtc.AccessAuthorizer
	Accounts       *account.Service
	Bankroll       *bankroll.Service
	Rooms          *room.Service
	Tables         *tablemanager.Manager
	Chat           *chat.Service
	History        history.Store
	Readiness      func(context.Context) error
	AllowedOrigins []string
}

func NewHandler(logger *slog.Logger, options Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReadiness(options.Readiness))
	registerAccountRoutes(mux, options.Accounts)
	registerBankrollRoutes(mux, options.Accounts, options.Bankroll)
	registerAdminRoutes(mux, options.Accounts, options.Bankroll)
	registerRoomRoutes(mux, options.Accounts, options.Rooms, options.Tables)
	registerHistoryRoutes(mux, options.Accounts, options.History)
	mux.Handle("GET /ws", newWebSocketServer(logger, options))
	mux.Handle("POST /v1/trtc/credentials", trtcCredentialsHandler(options))
	return securityHeaders(configuredCORS(mux, options.AllowedOrigins))
}

func configuredCORS(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[strings.TrimSpace(origin)] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		_, explicitlyAllowed := allowed[origin]
		if explicitlyAllowed || isLocalDevelopmentOrigin(origin) {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			writer.Header().Add("Vary", "Origin")
			if request.Method == http.MethodOptions {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), geolocation=()")
		next.ServeHTTP(writer, request)
	})
}

func isLocalDevelopmentOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"status":"ok"}`))
}

func handleReadiness(check func(context.Context) error) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if check != nil {
			ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
			err := check(ctx)
			cancel()
			if err != nil {
				writeJSONError(writer, http.StatusServiceUnavailable, "not_ready")
				return
			}
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
	}
}

type trtcCredentialsRequest struct {
	UserID string `json:"userId"`
	RoomID string `json:"roomId"`
}

func trtcCredentialsHandler(options Options) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if options.TRTCIssuer == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "trtc_not_configured")
			return
		}
		var body trtcCredentialsRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		if !allowDebugCredentialRequest(
			request,
			options.TRTCDebugToken,
			options.TRTCAuthorizer == nil,
		) {
			if options.TRTCAuthorizer == nil {
				writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
				return
			}
			err := options.TRTCAuthorizer.AuthorizeVoice(
				request.Context(),
				readBearerToken(request.Header.Get("Authorization")),
				body.UserID,
				body.RoomID,
			)
			if err != nil {
				var accessError trtc.AccessError
				if errors.As(err, &accessError) && accessError.Code == "permission_denied" {
					writeJSONError(writer, http.StatusForbidden, accessError.Code)
				} else {
					writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
				}
				return
			}
		}

		credentials, err := options.TRTCIssuer.Issue(body.UserID, body.RoomID)
		if err != nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_identity")
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(writer).Encode(credentials)
	})
}

func allowDebugCredentialRequest(request *http.Request, expectedToken string, allowLoopback bool) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if allowLoopback && err == nil && net.ParseIP(host).IsLoopback() {
		return true
	}
	if expectedToken == "" {
		return false
	}
	providedToken := readBearerToken(request.Header.Get("Authorization"))
	return providedToken == expectedToken
}

func readBearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func writeJSONError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": code})
}
