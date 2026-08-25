package transport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/tablemanager"
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

func registerBankrollRoutes(mux *http.ServeMux, accounts *account.Service, chips *bankroll.Service) {
	mux.HandleFunc("GET /v1/bankroll", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if chips == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		snapshot, err := chips.Snapshot(request.Context(), user.UserID)
		if err != nil {
			writeBankrollError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
	})

	mux.HandleFunc("POST /v1/bankroll/top-ups", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if chips == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			RequestID string `json:"requestId"`
			Amount    int64  `json:"amount"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		snapshot, err := chips.TopUp(request.Context(), user.UserID, body.RequestID, body.Amount)
		if err != nil {
			writeBankrollError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
	})

	mux.HandleFunc("GET /v1/bankroll/entries", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if chips == nil {
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
		entries, err := chips.Entries(request.Context(), user.UserID, limit)
		if err != nil {
			writeBankrollError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"entries": entries})
	})
}

func registerRoomRoutes(mux *http.ServeMux, accounts *account.Service, rooms *room.Service, tables *tablemanager.Manager) {
	mux.HandleFunc("GET /v1/rooms/preview", func(writer http.ResponseWriter, request *http.Request) {
		if _, ok := authenticateRequest(writer, request, accounts); !ok {
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		value, err := rooms.Preview(request.Context(), request.URL.Query().Get("code"))
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

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
			SmallBlind int64       `json:"smallBlind"`
			BigBlind   int64       `json:"bigBlind"`
			MaxBuyIn   int64       `json:"maxBuyIn"`
			BuyIn      int64       `json:"buyIn"`
			RequestID  string      `json:"requestId"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		participant := room.Participant{UserID: user.UserID, DisplayName: user.DisplayName}
		var value room.Room
		var err error
		if body.RequestID == "" {
			value, err = rooms.Create(request.Context(), participant, body.Preset, body.MaxPlayers, body.Password)
		} else {
			value, err = rooms.CreateConfigured(request.Context(), participant, room.CreateOptions{
				Preset: body.Preset, MaxPlayers: body.MaxPlayers, Password: body.Password,
				SmallBlind: body.SmallBlind, BigBlind: body.BigBlind, MaxBuyIn: body.MaxBuyIn,
				BuyIn: body.BuyIn, RequestID: body.RequestID,
			})
		}
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
			Code      string `json:"code"`
			Password  string `json:"password"`
			BuyIn     int64  `json:"buyIn"`
			RequestID string `json:"requestId"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		participant := room.Participant{UserID: user.UserID, DisplayName: user.DisplayName}
		var value room.Room
		var err error
		if body.RequestID == "" {
			value, err = rooms.Join(request.Context(), participant, body.Code, body.Password)
		} else {
			value, err = rooms.JoinWithBuyIn(request.Context(), participant, room.JoinOptions{
				Code: body.Code, Password: body.Password, BuyIn: body.BuyIn, RequestID: body.RequestID,
			})
		}
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
		var closed bool
		var err error
		if tables == nil {
			closed, err = rooms.Leave(request.Context(), user.UserID)
		} else {
			closed, err = tables.Leave(request.Context(), user.UserID)
		}
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

func writeBankrollError(writer http.ResponseWriter, err error) {
	var bankrollError bankroll.Error
	if !errors.As(err, &bankrollError) {
		writeJSONError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	switch bankrollError.Code {
	case "insufficient_wallet_chips", "maximum_buy_in_exceeded":
		writeJSONError(writer, http.StatusConflict, bankrollError.Code)
	default:
		writeJSONError(writer, http.StatusBadRequest, bankrollError.Code)
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
