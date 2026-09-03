package transport

import (
	"net/http"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/room"
)

// registerRoomOwnerRoutes 提供房主的房间管理，以及任何成员都能查看的本房间净胜负。
//
// 踢人复用玩家自己离桌的同一条路径（tablemanager.Leave），不另写移除逻辑：
// 那条路径已经处理了弃牌者的延迟返还、房主转移、最后一人离开时关闭房间，
// 另写一份必然漏掉其中某几种状态。
func registerRoomOwnerRoutes(
	mux *http.ServeMux,
	accounts *account.Service,
	rooms *room.Service,
	tables *tablemanager.Manager,
	chips *bankroll.Service,
	guard *guards,
	disconnectUsers func(roomID string, userIDs []string),
) {
	// 本人在当前房间内的净胜负，供牌桌里的「战绩」窗口换算。
	mux.HandleFunc("GET /v1/rooms/current/result", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if rooms == nil || chips == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		current, err := rooms.Current(request.Context(), user.UserID)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		result, err := chips.RoomResult(request.Context(), user.UserID, current.RoomID)
		if err != nil {
			writeBankrollError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	})

	// 房主开关房间入口。只影响新加入者，房内成员不受影响。
	mux.HandleFunc("POST /v1/rooms/settings/join-lock", func(writer http.ResponseWriter, request *http.Request) {
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
			Locked bool `json:"locked"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		updated, err := rooms.SetJoinLocked(request.Context(), user.UserID, body.Locked)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"joinLocked": updated.JoinLocked})
	})

	// 房主把一名成员移出房间。
	mux.HandleFunc("POST /v1/rooms/members/{userID}/remove", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
			return
		}
		if rooms == nil || tables == nil || accounts == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		targetUserID := request.PathValue("userID")
		current, err := rooms.Current(request.Context(), user.UserID)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		// 管理员不可被房主踢出：房主是房间内的角色，管理员是服务器级角色，
		// 后者需要能进入任何房间处理纠纷。
		isAdministrator, err := accounts.IsAdministrator(request.Context(), targetUserID)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		if isAdministrator {
			writeJSONError(writer, http.StatusForbidden, "cannot_remove_administrator")
			return
		}
		if err := rooms.RemoveMember(request.Context(), user.UserID, targetUserID); err != nil {
			writeRoomError(writer, err)
			return
		}
		closed, err := tables.KickMember(request.Context(), user.UserID, targetUserID)
		if err != nil {
			writeLeaveError(writer, err)
			return
		}
		if disconnectUsers != nil {
			disconnectUsers(current.RoomID, []string{targetUserID})
		}
		writeJSON(writer, http.StatusOK, map[string]any{"closed": closed})
	})
}
