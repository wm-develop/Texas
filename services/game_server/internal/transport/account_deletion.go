package transport

import (
	"errors"
	"net/http"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/room"
)

// registerAccountDeletionRoute 提供用户自行注销。
//
// 产品规则（2026-09-01 确定）：
//   - 注销前必须不在任何房间内；
//   - 钱包内全部筹码转入创建时间最早的在用管理员钱包，双方流水的
//     reference_id 记录被注销账号的 user_id；
//   - 注销后原用户名可以被重新注册；
//   - 管理员不能自行注销。
//
// 执行顺序是「先转筹码、后删账号」：筹码转移按 requestID 幂等，若删账号一步
// 失败，用户重试不会重复转账；反过来若先删账号再转账失败，筹码会滞留在已
// 注销账号里，因此不采用。
func registerAccountDeletionRoute(
	mux *http.ServeMux,
	accounts *account.Service,
	chips *bankroll.Service,
	rooms *room.Service,
	guard *guards,
) {
	mux.HandleFunc("POST /v1/users/me/delete", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if !guard.allow(writer, guard.userOps, user.UserID, "user_ops") {
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}

		// 密码错误计入与登录相同的按用户名锁定，防止把本接口当作猜密码入口
		usernameKey := normalizeUsername(user.Username)
		if guard != nil && guard.loginFail != nil && !guard.loginFail.Peek(usernameKey) {
			guard.reject(writer, guard.loginFail, usernameKey, "login_failures_user")
			return
		}
		if err := accounts.VerifyOwnPassword(user, body.Password); err != nil {
			if guard != nil {
				guard.loginFail.Allow(usernameKey)
			}
			writeAccountError(writer, err)
			return
		}
		if user.Role == account.RoleAdmin {
			writeJSONError(writer, http.StatusForbidden, "protected_account")
			return
		}
		if rooms != nil {
			if _, err := rooms.Current(request.Context(), user.UserID); err == nil {
				writeJSONError(writer, http.StatusConflict, "user_in_room")
				return
			}
		}

		var transferred int64
		recipientUserID := ""
		if chips != nil {
			snapshot, err := chips.Snapshot(request.Context(), user.UserID)
			if err != nil {
				writeBankrollError(writer, err)
				return
			}
			if snapshot.TableID != "" || snapshot.TableChips != 0 {
				writeJSONError(writer, http.StatusConflict, "user_in_room")
				return
			}
			recipient, err := accounts.EarliestActiveAdmin(request.Context())
			if err != nil {
				var accountError account.Error
				if errors.As(err, &accountError) && accountError.Code == "admin_unavailable" {
					writeJSONError(writer, http.StatusConflict, accountError.Code)
					return
				}
				writeAccountError(writer, err)
				return
			}
			recipientUserID = recipient.UserID
			transferred = snapshot.WalletChips
			if _, err := chips.TransferWallet(
				request.Context(), user.UserID, recipient.UserID,
				"account_deletion:"+user.UserID, user.UserID,
			); err != nil {
				writeBankrollError(writer, err)
				return
			}
		}

		if err := accounts.SelfDelete(request.Context(), user, recipientUserID, transferred); err != nil {
			writeAccountError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
}
