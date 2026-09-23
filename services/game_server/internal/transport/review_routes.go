package transport

import (
	"errors"
	"log/slog"
	"net/http"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/review"
)

// registerReviewRoutes 提供 AI 复盘：玩家为自己打过的一手发起复盘、查看结果；
// 管理员开通名单、调整全局设置、查看用量。
func registerReviewRoutes(mux *http.ServeMux, logger *slog.Logger, accounts *account.Service, reviews *review.Service) {
	// 当前账号能不能用复盘，客户端据此决定显不显示入口。
	mux.HandleFunc("GET /v1/review/access", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		available := false
		if reviews != nil {
			var err error
			if available, err = reviews.Available(request.Context(), user.UserID); err != nil {
				logReviewError(logger, "review access could not be read", err)
				writeJSONError(writer, http.StatusInternalServerError, "internal_error")
				return
			}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"available": available})
	})

	mux.HandleFunc("POST /v1/hands/{handID}/review", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if reviews == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "review_unavailable")
			return
		}
		value, err := reviews.Request(request.Context(), user.UserID, request.PathValue("handID"))
		if err != nil {
			writeReviewError(writer, logger, err)
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

	mux.HandleFunc("GET /v1/hands/{handID}/review", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		if reviews == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "review_unavailable")
			return
		}
		value, err := reviews.Get(request.Context(), user.UserID, request.PathValue("handID"))
		if err != nil {
			writeReviewError(writer, logger, err)
			return
		}
		writeJSON(writer, http.StatusOK, value)
	})

	mux.HandleFunc("GET /v1/admin/review", func(writer http.ResponseWriter, request *http.Request) {
		if _, ok := authorizeAdminRequest(writer, request, accounts); !ok {
			return
		}
		if reviews == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		overview, err := reviews.Overview(request.Context())
		if err != nil {
			writeReviewError(writer, logger, err)
			return
		}
		writeJSON(writer, http.StatusOK, overview)
	})

	mux.HandleFunc("POST /v1/admin/review/settings", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authorizeAdminRequest(writer, request, accounts)
		if !ok {
			return
		}
		if reviews == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		// 指针区分「没传」与「传了 false / 0」：漏传一个字段不能被当成关掉或不限。
		var body struct {
			Enabled            *bool  `json:"enabled"`
			DailyLimitPerUser  *int   `json:"dailyLimitPerUser"`
			MonthlyTokenBudget *int64 `json:"monthlyTokenBudget"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		if body.Enabled == nil || body.DailyLimitPerUser == nil || body.MonthlyTokenBudget == nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_review_settings")
			return
		}
		settings := review.Settings{
			Enabled: *body.Enabled, DailyLimitPerUser: *body.DailyLimitPerUser, MonthlyTokenBudget: *body.MonthlyTokenBudget,
		}
		if err := reviews.UpdateSettings(request.Context(), settings, actor.UserID); err != nil {
			writeReviewError(writer, logger, err)
			return
		}
		if err := accounts.RecordReviewSettingsChange(request.Context(), actor, map[string]any{
			"enabled": settings.Enabled, "dailyLimitPerUser": settings.DailyLimitPerUser,
			"monthlyTokenBudget": settings.MonthlyTokenBudget,
		}); err != nil {
			logReviewError(logger, "review settings change was saved but its audit record was not written", err)
		}
		writeJSON(writer, http.StatusOK, settings)
	})

	mux.HandleFunc("POST /v1/admin/review/access", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authorizeAdminRequest(writer, request, accounts)
		if !ok {
			return
		}
		if reviews == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var body struct {
			UserID  string `json:"userId"`
			Granted *bool  `json:"granted"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		if body.UserID == "" || body.Granted == nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		if _, err := accounts.ManagedUser(request.Context(), actor, body.UserID); err != nil {
			writeAccountError(writer, err)
			return
		}
		if err := reviews.SetAccess(request.Context(), body.UserID, *body.Granted, actor.UserID); err != nil {
			writeReviewError(writer, logger, err)
			return
		}
		if err := accounts.RecordReviewAccessChange(request.Context(), actor, body.UserID, *body.Granted); err != nil {
			logReviewError(logger, "review access change was saved but its audit record was not written", err)
		}
		writeJSON(writer, http.StatusOK, map[string]any{"userId": body.UserID, "granted": *body.Granted})
	})
}

func authorizeAdminRequest(writer http.ResponseWriter, request *http.Request, accounts *account.Service) (account.User, bool) {
	actor, ok := authenticateRequest(writer, request, accounts)
	if !ok {
		return account.User{}, false
	}
	if err := accounts.AuthorizeAdmin(actor); err != nil {
		writeAccountError(writer, err)
		return account.User{}, false
	}
	return actor, true
}

func writeReviewError(writer http.ResponseWriter, logger *slog.Logger, err error) {
	var reviewError review.Error
	switch {
	case errors.Is(err, review.ErrNotFound):
		writeJSONError(writer, http.StatusNotFound, "review_not_found")
	case errors.As(err, &reviewError):
		status := http.StatusBadRequest
		switch reviewError.Code {
		case "review_unavailable":
			status = http.StatusServiceUnavailable
		case "review_not_allowed":
			status = http.StatusForbidden
		case "hand_not_found":
			status = http.StatusNotFound
		case "review_daily_limit", "review_budget_exhausted":
			status = http.StatusTooManyRequests
		case "review_no_decisions", "replay_unavailable":
			status = http.StatusUnprocessableEntity
		}
		writeJSONError(writer, status, reviewError.Code)
	default:
		logReviewError(logger, "review request failed", err)
		writeJSONError(writer, http.StatusInternalServerError, "internal_error")
	}
}

func logReviewError(logger *slog.Logger, message string, err error) {
	if logger != nil {
		logger.Error(message, "error", err)
	}
}
