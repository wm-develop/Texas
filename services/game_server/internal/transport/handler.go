package transport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"texas/services/game_server/internal/account"
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
	Rooms          *room.Service
	Tables         *tablemanager.Manager
	Chat           *chat.Service
	History        history.Store
}

func NewHandler(logger *slog.Logger, options Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	registerAccountRoutes(mux, options.Accounts)
	registerRoomRoutes(mux, options.Accounts, options.Rooms)
	registerHistoryRoutes(mux, options.Accounts, options.History)
	mux.Handle("GET /ws", newWebSocketServer(logger, options))
	mux.Handle("POST /v1/trtc/credentials", trtcCredentialsHandler(options))
	return localDevelopmentCORS(mux)
}

func localDevelopmentCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if isLocalDevelopmentOrigin(origin) {
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
