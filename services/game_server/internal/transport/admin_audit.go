package transport

import (
	"net/http"
	"strconv"

	"texas/services/game_server/internal/account"
)

// auditUserResponse 让客户端把审计里的 user_id 显示为用户名，而不必再查一次用户列表。
type auditUserResponse struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

// registerAdminAuditRoute 提供管理员审计查询：
//
//	GET /v1/admin/audit?limit=100&userId=usr_xxx
//
// 返回按时间倒序的审计事件，以及事件中出现过的所有 user_id 到用户名的映射。
func registerAdminAuditRoute(mux *http.ServeMux, accounts *account.Service) {
	mux.HandleFunc("GET /v1/admin/audit", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		events, err := accounts.ListAudit(request.Context(), actor, account.AuditQuery{
			UserID: request.URL.Query().Get("userId"), Limit: limit,
		})
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		users, err := accounts.ListUsers(request.Context(), actor)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		byID := make(map[string]account.User, len(users))
		for _, user := range users {
			byID[user.UserID] = user
		}
		referenced := make(map[string]auditUserResponse)
		mention := func(userID string) {
			if user, found := byID[userID]; found && userID != "" {
				referenced[userID] = auditUserResponse{Username: user.Username, DisplayName: user.DisplayName}
			}
		}
		for _, event := range events {
			mention(event.ActorUserID)
			for _, key := range []string{"targetUserId", "recipientUserId"} {
				if value, _ := event.Metadata[key].(string); value != "" {
					mention(value)
				}
			}
			switch targets := event.Metadata["targetUserIds"].(type) {
			case []string:
				for _, target := range targets {
					mention(target)
				}
			case []any:
				for _, target := range targets {
					if value, _ := target.(string); value != "" {
						mention(value)
					}
				}
			}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"events": events, "users": referenced})
	})
}
