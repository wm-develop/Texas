package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/config"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
	"texas/services/game_server/internal/transport"
	"texas/services/game_server/internal/trtc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	appConfig, err := config.Load()
	if err != nil {
		logger.Error("invalid server configuration", "error", err)
		os.Exit(1)
	}
	address := ":" + appConfig.Port
	passwordHasher, err := security.NewPasswordHasher(security.DefaultPasswordIterations, nil)
	if err != nil {
		logger.Error("password hasher initialization failed", "error", err)
		os.Exit(1)
	}
	accountService, err := account.NewService(
		account.NewMemoryRepository(),
		passwordHasher,
		account.ServiceConfig{AccessTTL: 24 * time.Hour, RefreshTTL: 30 * 24 * time.Hour},
	)
	if err != nil {
		logger.Error("account service initialization failed", "error", err)
		os.Exit(1)
	}
	bankrollService, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		logger.Error("bankroll service initialization failed", "error", err)
		os.Exit(1)
	}
	roomService, err := room.NewService(room.NewMemoryRepository(), passwordHasher, room.ServiceConfig{Bankroll: bankrollService})
	if err != nil {
		logger.Error("room service initialization failed", "error", err)
		os.Exit(1)
	}
	ledgerStore := ledger.NewInMemoryStore()
	historyStore := history.NewInMemoryStore()
	tableManager, err := tablemanager.NewWithConfig(roomService, holdem.CryptoRandom{}, tablemanager.ManagerConfig{
		Ledger: ledgerStore, History: historyStore, Bankroll: bankrollService,
	})
	if err != nil {
		logger.Error("table manager initialization failed", "error", err)
		os.Exit(1)
	}
	chatService, err := chat.NewService(chat.Policy{
		MaximumRunes: 200, MaximumPerWindow: 5, RateWindow: 10 * time.Second, HistoryLimit: 50,
		AllowedQuickTexts: map[string]struct{}{
			"好牌": {}, "快一点": {}, "再来一局": {}, "运气不错": {},
		},
		AllowedEmoji: map[string]struct{}{
			"👍": {}, "👏": {}, "😂": {}, "😮": {}, "🤝": {},
		},
	}, time.Now, randomChatID)
	if err != nil {
		logger.Error("chat service initialization failed", "error", err)
		os.Exit(1)
	}

	var credentialIssuer trtc.CredentialIssuer
	if appConfig.TRTCEnabled() {
		credentialIssuer = trtc.NewTencentIssuer(
			appConfig.TRTCSDKAppID,
			appConfig.TRTCSecretKey,
			appConfig.TRTCExpire,
		)
		logger.Info("TRTC credential issuer enabled")
	}

	server := &http.Server{
		Addr: address,
		Handler: transport.NewHandler(logger, transport.Options{
			TRTCIssuer:     credentialIssuer,
			TRTCDebugToken: appConfig.TRTCDebugToken,
			TRTCAuthorizer: trtc.MembershipAuthorizer{
				Sessions: accountService, Membership: roomService,
			},
			Accounts: accountService,
			Bankroll: bankrollService,
			Rooms:    roomService,
			Tables:   tableManager,
			Chat:     chatService,
			History:  historyStore,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("server shutdown failed", "error", err)
		}
	}()

	logger.Info("game server listening", "address", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("game server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

func randomChatID() string {
	value := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return ""
	}
	return "msg_" + base64.RawURLEncoding.EncodeToString(value)
}
