package transport

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/room"
)

// adminRoomResponse 是管理员后台房间列表里的一行。
type adminRoomResponse struct {
	RoomID         string            `json:"roomId"`
	RoomCode       string            `json:"roomCode"`
	OwnerName      string            `json:"ownerName"`
	SmallBlind     int64             `json:"smallBlind"`
	BigBlind       int64             `json:"bigBlind"`
	SeatedCount    int               `json:"seatedCount"`
	SpectatorCount int               `json:"spectatorCount"`
	Rake           room.RakeSettings `json:"rake"`
	RakeTotal      int64             `json:"rakeTotal"`
	RakeHands      int               `json:"rakeHands"`
}

// registerAdminRakeRoutes 提供管理员的抽水管理：列出开着的房间、设置某个房间的抽水规则、
// 按房间查看累计抽水。抽水规则只有管理员能改，房主不能。
//
// announce 把一条聊天消息广播给房间里的人：规则一变就在房间聊天里公告，玩家不必
// 去任何设置页里找，也不占牌桌界面的空间。
func registerAdminRakeRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	accounts *account.Service,
	rooms *room.Service,
	chips *bankroll.Service,
	chatService *chat.Service,
	announce func(roomID string, message chat.Message),
) {
	mux.HandleFunc("GET /v1/admin/rooms", func(writer http.ResponseWriter, request *http.Request) {
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
		values, err := rooms.ListOpen(request.Context())
		if err != nil {
			writeJSONError(writer, http.StatusInternalServerError, "internal_error")
			return
		}
		totals := make(map[string]bankroll.RoomRake)
		if chips != nil {
			summary, err := chips.RakeByRoom(request.Context())
			if err != nil {
				writeBankrollError(writer, err)
				return
			}
			for _, entry := range summary {
				totals[entry.RoomID] = entry
			}
		}
		result := make([]adminRoomResponse, 0, len(values))
		for _, value := range values {
			row := adminRoomResponse{
				RoomID: value.RoomID, RoomCode: value.Code,
				SmallBlind: value.Rules.SmallBlind, BigBlind: value.Rules.BigBlind,
				SeatedCount: len(value.SeatedMembers()), SpectatorCount: len(value.SpectatorMembers()),
				Rake: value.Rake, RakeTotal: totals[value.RoomID].Total, RakeHands: totals[value.RoomID].Hands,
			}
			for _, member := range value.Members {
				if member.UserID == value.OwnerUserID {
					row.OwnerName = member.DisplayName
				}
			}
			result = append(result, row)
		}
		writeJSON(writer, http.StatusOK, map[string]any{"rooms": result})
	})

	mux.HandleFunc("POST /v1/admin/rooms/{roomID}/rake", func(writer http.ResponseWriter, request *http.Request) {
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
		// 指针区分「没传」与「传了 false / 0」：漏传一个字段不能被当成把它关掉。
		var body struct {
			Enabled         *bool  `json:"enabled"`
			BasisPoints     *int   `json:"basisPoints"`
			Cap             *int64 `json:"cap"`
			PostflopEnabled *bool  `json:"postflopEnabled"`
			PostflopAmount  *int64 `json:"postflopAmount"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		if body.Enabled == nil || body.BasisPoints == nil || body.Cap == nil ||
			body.PostflopEnabled == nil || body.PostflopAmount == nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_rake_settings")
			return
		}
		settings := room.RakeSettings{
			Enabled: *body.Enabled, BasisPoints: *body.BasisPoints, Cap: *body.Cap,
			PostflopEnabled: *body.PostflopEnabled, PostflopAmount: *body.PostflopAmount,
		}
		updated, changed, err := rooms.UpdateRakeSettings(request.Context(), request.PathValue("roomID"), settings)
		if err != nil {
			writeRoomError(writer, err)
			return
		}
		if changed {
			// 先公告再记审计：规则已经落库、下一手就生效，审计写失败也不能让玩家蒙在鼓里。
			// 重试时规则没变，不会再走到这里，所以公告只有这一次机会。
			// 公告发不出去不回滚设置：结算页每手都会写明抽了多少，牌桌信息栏也显示当前规则。
			if chatService != nil && announce != nil {
				if message, err := chatService.Announce(actor.UserID, "系统公告", updated.RoomID, rakeAnnouncement(settings)); err == nil {
					announce(updated.RoomID, message)
				}
			}
			if err := accounts.RecordManagedRakeChange(request.Context(), actor, updated.RoomID, updated.Code, map[string]any{
				"enabled": settings.Enabled, "basisPoints": settings.BasisPoints, "cap": settings.Cap,
				"postflopEnabled": settings.PostflopEnabled, "postflopAmount": settings.PostflopAmount,
			}); err != nil && logger != nil {
				// 规则已经落库并公告，这时回一个错误只会让管理员以为没改成；重试时规则
				// 没变也不会再记审计。所以照常回 200，把缺的这条审计写进错误日志。
				logger.Error("rake change was saved but its audit record was not written",
					"roomId", updated.RoomID, "actorUserId", actor.UserID, "error", err,
					"enabled", settings.Enabled, "basisPoints", settings.BasisPoints, "cap", settings.Cap,
					"postflopEnabled", settings.PostflopEnabled, "postflopAmount", settings.PostflopAmount)
			}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"roomId": updated.RoomID, "roomCode": updated.Code, "rake": updated.Rake})
	})

	mux.HandleFunc("GET /v1/admin/rake", func(writer http.ResponseWriter, request *http.Request) {
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
		summary, err := chips.RakeByRoom(request.Context())
		if err != nil {
			writeBankrollError(writer, err)
			return
		}
		// 内存仓储不知道房间码；开着的房间从房间服务补上。
		if rooms != nil {
			if open, err := rooms.ListOpen(request.Context()); err == nil {
				codes := make(map[string]string, len(open))
				for _, value := range open {
					codes[value.RoomID] = value.Code
				}
				for index := range summary {
					if code, isOpen := codes[summary[index].RoomID]; isOpen {
						summary[index].RoomCode = code
						summary[index].Closed = false
					} else if summary[index].RoomCode == "" {
						summary[index].Closed = true
					}
				}
			}
		}
		var total int64
		var hands int
		for _, entry := range summary {
			total += entry.Total
			hands += entry.Hands
		}
		if summary == nil {
			summary = []bankroll.RoomRake{}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"rooms": summary, "total": total, "hands": hands})
	})
}

// rakeAnnouncement 是规则变更后发到房间聊天里的公告，要让没看过任何设置页的玩家
// 一眼看懂从下一手起会被抽多少。
func rakeAnnouncement(settings room.RakeSettings) string {
	if !settings.Enabled {
		return "管理员已关闭本房间的抽水，下一手起不再抽水。"
	}
	var parts []string
	if settings.BasisPoints > 0 {
		part := "每手按底池的 " + formatBasisPoints(settings.BasisPoints) + " 抽水"
		if settings.Cap > 0 {
			part += fmt.Sprintf("，最多 %d", settings.Cap)
		} else {
			part += "，不设上限"
		}
		parts = append(parts, part)
	}
	if settings.PostflopEnabled && settings.PostflopAmount > 0 {
		parts = append(parts, fmt.Sprintf("发出翻牌的手再加抽 %d（底池不足两个大盲时不加）", settings.PostflopAmount))
	}
	if len(parts) == 0 {
		return "管理员已开启本房间的抽水，但比例与加抽均为 0，实际不抽。"
	}
	return "管理员已调整本房间的抽水，下一手起生效：" + strings.Join(parts, "；") +
		"。没人跟注而退回的筹码不计入底池；每手抽了多少会写在结算里。"
}

// formatBasisPoints 把万分比写成百分数：250 → 2.5%，500 → 5%。
func formatBasisPoints(basisPoints int) string {
	whole, fraction := basisPoints/100, basisPoints%100
	if fraction == 0 {
		return strconv.Itoa(whole) + "%"
	}
	text := fmt.Sprintf("%d.%02d", whole, fraction)
	return strings.TrimRight(text, "0") + "%"
}
