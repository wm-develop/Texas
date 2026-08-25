package transport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
)

func registerAccountRoutes(mux *http.ServeMux, accounts *account.Service) {
	mux.HandleFunc("POST /v1/auth/register", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			Password    string `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		result, err := accounts.Register(request.Context(), body.Username, body.DisplayName, body.Password)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, result)
	})

	mux.HandleFunc("POST /v1/auth/login", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		result, err := accounts.Login(request.Context(), body.Username, body.Password)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	})

	mux.HandleFunc("POST /v1/auth/refresh", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			RefreshToken string `json:"refreshToken"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		result, err := accounts.Refresh(request.Context(), body.RefreshToken)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	})

	mux.HandleFunc("GET /v1/users/me", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		writeJSON(writer, http.StatusOK, user)
	})
}

func registerRoomRoutes(mux *http.ServeMux, accounts *account.Service, rooms *room.Service) {
	mux.HandleFunc("POST /v1/rooms", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			Preset     room.Preset `json:"preset"`
			MaxPlayers int         `json:"maxPlayers"`
			Password   string      `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		value, err := rooms.Create(request.Context(), room.Participant{
			UserID: user.UserID, DisplayName: user.DisplayName,
		}, body.Preset, body.MaxPlayers, body.Password)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, value)
	})

	mux.HandleFunc("POST /v1/rooms/join", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			Code     string `json:"code"`
			Password string `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		value, err := rooms.Join(request.Context(), room.Participant{
			UserID: user.UserID, DisplayName: user.DisplayName,
		}, body.Code, body.Password)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

	mux.HandleFunc("GET /v1/rooms/current", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		value, err := rooms.Current(request.Context(), user.UserID)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

	mux.HandleFunc("POST /v1/rooms/ready", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			Ready bool `json:"ready"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		value, err := rooms.SetReady(request.Context(), user.UserID, body.Ready)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

	mux.HandleFunc("POST /v1/rooms/leave", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		closed, err := rooms.Leave(request.Context(), user.UserID)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"closed": closed})
	})
}

func registerHistoryRoutes(mux *http.ServeMux, accounts *account.Service, hands history.Store) {
	mux.HandleFunc("GET /v1/hands/recent", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if hands == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		limit := 20
		if raw := request.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 || parsed > 100 {
				writeJSONError(writer, http.StatusBadRequest, "invalid_request")
				return
			}
			limit = parsed
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"hands": hands.RecentForPlayer(user.UserID, limit),
		})
	})
}

func authenticateRequest(
	writer http.ResponseWriter,
	request *http.Request,
	accounts *account.Service,
) (account.User, bool) {
	if accounts == nil {
		writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
		return account.User{}, false
	}
	user, err := accounts.Authenticate(request.Context(), readBearerToken(request.Header.Get("Authorization")))
	if err != nil {
		writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
		return account.User{}, false
	}
	return user, true
}

func decodeJSONBody(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func writeAccountError(writer http.ResponseWriter, err error) {
	var accountError account.Error
	if !errors.As(err, &accountError) {
		writeJSONError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	switch accountError.Code {
	case "username_taken":
		writeJSONError(writer, http.StatusConflict, accountError.Code)
	case "invalid_credentials", "authentication_required", "invalid_refresh_token":
		writeJSONError(writer, http.StatusUnauthorized, accountError.Code)
	default:
		writeJSONError(writer, http.StatusBadRequest, accountError.Code)
	}
}

func writeRoomError(writer http.ResponseWriter, err error) {
	var roomError room.Error
	if !errors.As(err, &roomError) {
		writeJSONError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	switch roomError.Code {
	case "room_not_found":
		writeJSONError(writer, http.StatusNotFound, roomError.Code)
	case "already_in_room", "room_full":
		writeJSONError(writer, http.StatusConflict, roomError.Code)
	case "permission_denied":
		writeJSONError(writer, http.StatusForbidden, roomError.Code)
	default:
		writeJSONError(writer, http.StatusBadRequest, roomError.Code)
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func bearerHeader(token string) string {
	return "Bearer " + strings.TrimSpace(token)
}
