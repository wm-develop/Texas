package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
)

func registerAccountRoutes(
	mux *http.ServeMux,
	accounts *account.Service,
	presence *presenceTracker,
	guard *guards,
) {
	mux.HandleFunc("POST /v1/auth/register", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		ip := guard.clientIP(request)
		if !guard.allow(writer, guard.auth, ip, "auth_ip") ||
			!guard.allow(writer, guard.register, ip, "register_ip") {
			return
		}
		var body struct {
			Username     string `json:"username"`
			DisplayName  string `json:"displayName"`
			Password     string `json:"password"`
			RequestAdmin bool   `json:"requestAdmin"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		result, err := accounts.RegisterWithOptions(
			request.Context(), body.Username, body.DisplayName, body.Password,
			account.RegistrationOptions{RequestInitialAdmin: body.RequestAdmin},
		)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		presence.touch(result.User.UserID)
		writeJSON(writer, http.StatusCreated, result)
	})

	mux.HandleFunc("POST /v1/auth/login", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		if !guard.allow(writer, guard.auth, guard.clientIP(request), "auth_ip") {
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		// 按用户名的失败计数：先看是否已被锁定，只在密码错误时消耗配额，
		// 成功后清零。这样暴力猜测某个账号会被单独拦住，而不影响其他人登录。
		usernameKey := normalizeUsername(body.Username)
		if guard != nil && guard.loginFail != nil && !guard.loginFail.Peek(usernameKey) {
			guard.reject(writer, guard.loginFail, usernameKey, "login_failures_user")
			return
		}
		result, err := accounts.Login(request.Context(), body.Username, body.Password)
		if err != nil {
			var accountError account.Error
			if guard != nil && errors.As(err, &accountError) && accountError.Code == "invalid_credentials" {
				guard.loginFail.Allow(usernameKey)
			}
			writeAccountError(writer, err)
			return
		}
		if guard != nil {
			guard.loginFail.Reset(usernameKey)
		}
		presence.touch(result.User.UserID)
		writeJSON(writer, http.StatusOK, result)
	})

	mux.HandleFunc("POST /v1/auth/refresh", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		if !guard.allow(writer, guard.auth, guard.clientIP(request), "auth_ip") {
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
		presence.touch(result.User.UserID)
		writeJSON(writer, http.StatusOK, result)
	})

	mux.HandleFunc("POST /v1/auth/logout", func(writer http.ResponseWriter, request *http.Request) {
		if accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		if err := accounts.Logout(request.Context(), readBearerToken(request.Header.Get("Authorization"))); err != nil {
			writeAccountError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /v1/users/me", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		writeJSON(writer, http.StatusOK, user)
	})

	mux.HandleFunc("POST /v1/users/me/heartbeat", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		presence.touch(user.UserID)
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /v1/users/me/username", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			Username string `json:"username"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		updated, err := accounts.UpdateOwnUsername(request.Context(), user, body.Username)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		presence.touch(user.UserID)
		writeJSON(writer, http.StatusOK, updated)
	})

	mux.HandleFunc("POST /v1/users/me/display-name", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			DisplayName string `json:"displayName"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		updated, err := accounts.UpdateOwnDisplayName(
			request.Context(), user, body.DisplayName,
		)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		presence.touch(user.UserID)
		writeJSON(writer, http.StatusOK, updated)
	})

	mux.HandleFunc("POST /v1/users/me/password", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		result, err := accounts.ChangeOwnPassword(
			request.Context(), user, body.CurrentPassword, body.NewPassword,
		)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		presence.touch(user.UserID)
		writeJSON(writer, http.StatusOK, result)
	})
}

type managedUserResponse struct {
	UserID      string         `json:"userId"`
	Username    string         `json:"username"`
	DisplayName string         `json:"displayName"`
	Role        account.Role   `json:"role"`
	Status      account.Status `json:"status"`
	WalletChips int64          `json:"walletChips"`
	TableChips  int64          `json:"tableChips"`
	TableID     string         `json:"tableId,omitempty"`
	RoomCode    string         `json:"roomCode,omitempty"`
	Online      bool           `json:"online"`
	ChatMuted   bool           `json:"chatMuted"`
	CreatedAt   time.Time      `json:"createdAt"`
}

func registerAdminRoutes(
	mux *http.ServeMux,
	accounts *account.Service,
	chips *bankroll.Service,
	rooms *room.Service,
	tables *tablemanager.Manager,
	chatService *chat.Service,
	presence *presenceTracker,
	disconnectUsers func(roomID string, userIDs []string, reason string),
) {
	mux.HandleFunc("GET /v1/admin/users", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		users, err := accounts.ListUsers(request.Context(), actor)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		if chips == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		managed := make([]managedUserResponse, 0, len(users))
		for _, user := range users {
			snapshot, err := chips.Snapshot(request.Context(), user.UserID)
			if err != nil {
				writeBankrollError(writer, err)
				return
			}
			roomCode := ""
			if snapshot.TableID != "" && rooms != nil {
				current, currentErr := rooms.Current(request.Context(), user.UserID)
				if currentErr == nil {
					roomCode = current.Code
				}
			}
			chatMuted := false
			if chatService != nil {
				chatMuted, err = chatService.IsMuted(user.UserID)
				if err != nil {
					writeJSONError(writer, http.StatusInternalServerError, "internal_error")
					return
				}
			}
			managed = append(managed, managedUserResponse{
				UserID: user.UserID, Username: user.Username, DisplayName: user.DisplayName,
				Role: user.Role, Status: user.Status, CreatedAt: user.CreatedAt,
				WalletChips: snapshot.WalletChips, TableChips: snapshot.TableChips,
				TableID: snapshot.TableID, RoomCode: roomCode,
				Online: presence.online(user.UserID), ChatMuted: chatMuted,
			})
		}
		writeJSON(writer, http.StatusOK, map[string]any{"users": managed})
	})

	mux.HandleFunc("POST /v1/admin/users/{userID}/chat-mute", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if err := accounts.AuthorizeAdmin(actor); err != nil {
			writeAccountError(writer, err)
			return
		}
		if chatService == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		target, err := accounts.ManagedUser(
			request.Context(), actor, request.PathValue("userID"),
		)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		if target.Role == account.RoleAdmin {
			writeJSONError(writer, http.StatusForbidden, "protected_account")
			return
		}
		var body struct {
			Muted bool `json:"muted"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		if err := chatService.SetMuted(actor.UserID, target.UserID, body.Muted); err != nil {
			writeJSONError(writer, http.StatusInternalServerError, "internal_error")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"muted": body.Muted})
	})

	mux.HandleFunc("POST /v1/admin/users", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
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
		user, err := accounts.CreateManagedUser(
			request.Context(), actor, body.Username, body.DisplayName, body.Password,
		)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, user)
	})

	mux.HandleFunc("POST /v1/admin/users/status", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			UserIDs []string       `json:"userIds"`
			Status  account.Status `json:"status"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		if body.Status != account.StatusActive && chips != nil {
			for _, userID := range body.UserIDs {
				snapshot, err := chips.Snapshot(request.Context(), userID)
				if err != nil {
					writeBankrollError(writer, err)
					return
				}
				if snapshot.TableID != "" {
					writeJSONError(writer, http.StatusConflict, "user_in_room")
					return
				}
			}
		}
		if err := accounts.UpdateManagedStatuses(
			request.Context(), actor, body.UserIDs, body.Status,
		); err != nil {
			writeAccountError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /v1/admin/users/{userID}/password", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		if err := accounts.ResetManagedPassword(
			request.Context(), actor, request.PathValue("userID"), body.Password,
		); err != nil {
			writeAccountError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /v1/admin/users/{userID}/username", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			Username string `json:"username"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		updated, err := accounts.UpdateManagedUsername(
			request.Context(), actor, request.PathValue("userID"), body.Username,
		)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	})

	mux.HandleFunc("POST /v1/admin/users/{userID}/wallet", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if err := accounts.AuthorizeAdmin(actor); err != nil {
			writeAccountError(writer, err)
			return
		}
		if chips == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		if _, err := accounts.ManagedUser(
			request.Context(), actor, request.PathValue("userID"),
		); err != nil {
			writeAccountError(writer, err)
			return
		}
		var body struct {
			RequestID string `json:"requestId"`
			Chips     int64  `json:"chips"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		snapshot, err := chips.SetWallet(
			request.Context(), request.PathValue("userID"), body.RequestID, body.Chips,
		)
		if err != nil {
			writeBankrollError(writer, err)
			return
		}
		if err := accounts.RecordManagedWalletChange(
			request.Context(), actor, request.PathValue("userID"), snapshot.WalletChips,
		); err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
	})

	mux.HandleFunc("POST /v1/admin/users/{userID}/leave-room", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if err := accounts.AuthorizeAdmin(actor); err != nil {
			writeAccountError(writer, err)
			return
		}
		if rooms == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		targetUserID := request.PathValue("userID")
		current, err := rooms.Current(request.Context(), targetUserID)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		var closed bool
		if tables == nil {
			closed, err = rooms.Leave(request.Context(), targetUserID)
		} else {
			closed, err = tables.Leave(request.Context(), targetUserID)
		}
		if err != nil {
			writeLeaveError(writer, err)
			return
		}
		if disconnectUsers != nil {
			userIDs := []string{targetUserID}
			if closed {
				userIDs = make([]string, 0, len(current.Members))
				for _, member := range current.Members {
					userIDs = append(userIDs, member.UserID)
				}
			}
			disconnectUsers(current.RoomID, userIDs, RemovedByAdministrator)
		}
		if err := accounts.RecordManagedRoomRemoval(
			request.Context(), actor, targetUserID, current.RoomID, current.Code,
		); err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"closed": closed})
	})

	mux.HandleFunc("GET /v1/admin/settings/registration", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		settings, err := accounts.RegistrationSettings(request.Context(), actor)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	})

	mux.HandleFunc("POST /v1/admin/settings/registration", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		settings, err := accounts.SetRegistrationEnabled(request.Context(), actor, body.Enabled)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	})
}

func registerBankrollRoutes(mux *http.ServeMux, accounts *account.Service, chips *bankroll.Service, guard *guards) {
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
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
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

func registerRoomRoutes(
	mux *http.ServeMux,
	accounts *account.Service,
	rooms *room.Service,
	tables *tablemanager.Manager,
	guard *guards,
) {
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
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
			return
		}
		// 抽水打进管理员钱包，管理员再上桌就既是收款方又是玩家，账目与立场都说不清。
		// 观战同样不行：付费观战能看到所有人的底牌。
		if user.Role == account.RoleAdmin {
			writeJSONError(writer, http.StatusForbidden, "admin_cannot_play")
			return
		}
		var body struct {
			Preset     room.Preset `json:"preset"`
			MaxPlayers int         `json:"maxPlayers"` // Deprecated; accepted but ignored.
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
		// 弃牌中途离开、上一手还没结算：此时他在数据上仍是原房间的成员。房主会被
		// Create 当成「重复创建」原样送回旧房间，其他人则撞上 already_in_room，
		// 两种都不是他想要的，直接说明原因。
		if _, pending := pendingLeaveRoom(request.Context(), rooms, tables, user.UserID); pending {
			writeJSONError(writer, http.StatusConflict, "leave_pending")
			return
		}
		var value room.Room
		var err error
		if body.RequestID == "" {
			value, err = rooms.Create(request.Context(), participant, body.Preset, room.MaximumPlayers, body.Password)
		} else {
			value, err = rooms.CreateConfigured(request.Context(), participant, room.CreateOptions{
				Preset: body.Preset, MaxPlayers: room.MaximumPlayers, Password: body.Password,
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
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
			return
		}
		// 抽水打进管理员钱包，管理员再上桌就既是收款方又是玩家，账目与立场都说不清。
		// 观战同样不行：付费观战能看到所有人的底牌。
		if user.Role == account.RoleAdmin {
			writeJSONError(writer, http.StatusForbidden, "admin_cannot_play")
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
		// 待离开期间加入**别的**房间要等上一手结算；回到原房间则放行——那等于
		// 撤销离开，筹码原样延续（见 tablemanager.Manager.Join）。
		if leaving, pending := pendingLeaveRoom(request.Context(), rooms, tables, user.UserID); pending &&
			leaving.Code != strings.TrimSpace(body.Code) {
			writeJSONError(writer, http.StatusConflict, "leave_pending")
			return
		}
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
		// 弃牌后中途离开的人，成员记录要留到本手结算（桌上筹码记在上面）。
		// 对他本人来说已经离开了：不隐藏的话，重启应用会被直接送回牌桌。
		if tables != nil && tables.LeavePending(user.UserID, value.RoomID) {
			writeJSONError(writer, http.StatusNotFound, "room_not_found")
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

	mux.HandleFunc("POST /v1/rooms/ready", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
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
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
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
	case "admin_already_initialized":
		writeJSONError(writer, http.StatusConflict, accountError.Code)
	case "registration_disabled", "admin_required", "protected_account":
		writeJSONError(writer, http.StatusForbidden, accountError.Code)
	case "user_not_found":
		writeJSONError(writer, http.StatusNotFound, accountError.Code)
	case "invalid_credentials", "invalid_current_password", "authentication_required", "invalid_refresh_token":
		writeJSONError(writer, http.StatusUnauthorized, accountError.Code)
	default:
		writeJSONError(writer, http.StatusBadRequest, accountError.Code)
	}
}

// pendingLeaveRoom 返回此人「已经点了离开、但要等本手结算才真正移出」的那个房间。
func pendingLeaveRoom(
	ctx context.Context, rooms *room.Service, tables *tablemanager.Manager, userID string,
) (room.Room, bool) {
	if rooms == nil || tables == nil {
		return room.Room{}, false
	}
	current, err := rooms.Current(ctx, userID)
	if err != nil || !tables.LeavePending(userID, current.RoomID) {
		return room.Room{}, false
	}
	return current, true
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
	case "insufficient_wallet_chips", "maximum_buy_in_exceeded", "user_in_room":
		writeJSONError(writer, http.StatusConflict, bankrollError.Code)
	default:
		writeJSONError(writer, http.StatusBadRequest, bankrollError.Code)
	}
}

func writeLeaveError(writer http.ResponseWriter, err error) {
	code := errorCode(err)
	switch code {
	case "hand_in_progress", "user_in_room":
		writeJSONError(writer, http.StatusConflict, code)
	case "room_not_found":
		writeJSONError(writer, http.StatusNotFound, code)
	default:
		writeJSONError(writer, http.StatusBadRequest, code)
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
