package main

import (
	"context"
	"crypto/rand"
	"database/sql"
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
	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
	"texas/services/game_server/internal/transport"
	"texas/services/game_server/internal/trtc"
	"texas/services/game_server/migrations"
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
	var (
		database           *sql.DB
		accountRepository  account.Repository  = account.NewMemoryRepository()
		bankrollRepository bankroll.Repository = bankroll.NewMemoryRepository()
		roomRepository     room.Repository     = room.NewMemoryRepository()
		ledgerStore        ledger.Store        = ledger.NewInMemoryStore()
		historyStore       history.Store       = history.NewInMemoryStore()
		chatStore          chat.Store
	)
	if appConfig.DatabaseEnabled() {
		connectContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		database, err = postgres.Open(connectContext, appConfig.DatabaseURL)
		cancel()
		if err != nil {
			logger.Error("database connection failed", "error", err)
			os.Exit(1)
		}
		defer database.Close()
		migrator, migrationErr := postgres.NewMigrator(migrations.Files)
		if migrationErr != nil {
			logger.Error("migration catalog is invalid", "error", migrationErr)
			os.Exit(1)
		}
		migrationContext, migrationCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if appConfig.AutoMigrate {
			_, migrationErr = migrator.Up(migrationContext, database)
		} else {
			migrationErr = migrator.Validate(migrationContext, database)
		}
		migrationCancel()
		if migrationErr != nil {
			logger.Error("database schema validation failed", "error", migrationErr)
			os.Exit(1)
		}
		accountRepository, err = account.NewPostgresRepository(database)
		if err == nil {
			bankrollRepository, err = bankroll.NewPostgresRepository(database)
		}
		if err == nil {
			roomRepository, err = room.NewPostgresRepository(database)
		}
		if err == nil {
			ledgerStore, err = ledger.NewPostgresStore(database)
		}
		if err == nil {
			historyStore, err = history.NewPostgresStore(database)
		}
		if err == nil {
			chatStore, err = chat.NewPostgresStore(database)
		}
		if err != nil {
			logger.Error("postgres repository initialization failed", "error", err)
			os.Exit(1)
		}
		logger.Info("postgres storage enabled")
	}
	accountService, err := account.NewService(
		accountRepository,
		passwordHasher,
		account.ServiceConfig{AccessTTL: 24 * time.Hour, RefreshTTL: 30 * 24 * time.Hour},
	)
	if err != nil {
		logger.Error("account service initialization failed", "error", err)
		os.Exit(1)
	}
	bankrollService, err := bankroll.NewService(bankrollRepository, time.Now)
	if err != nil {
		logger.Error("bankroll service initialization failed", "error", err)
		os.Exit(1)
	}
	roomService, err := room.NewService(roomRepository, passwordHasher, room.ServiceConfig{Bankroll: bankrollService})
	if err != nil {
		logger.Error("room service initialization failed", "error", err)
		os.Exit(1)
	}
	tableManager, err := tablemanager.NewWithConfig(roomService, holdem.CryptoRandom{}, tablemanager.ManagerConfig{
		Ledger: ledgerStore, History: historyStore, Bankroll: bankrollService,
	})
	if err != nil {
		logger.Error("table manager initialization failed", "error", err)
		os.Exit(1)
	}
	chatService, err := chat.NewServiceWithStore(chat.Policy{
		MaximumRunes: 200, MaximumPerWindow: 5, RateWindow: 10 * time.Second, HistoryLimit: 50,
		AllowedQuickTexts: map[string]struct{}{
			"好牌": {}, "快一点": {}, "再来一局": {}, "运气不错": {},
		},
		AllowedEmoji: map[string]struct{}{
			"👍": {}, "👏": {}, "😂": {}, "😮": {}, "🤝": {},
		},
	}, time.Now, randomChatID, chatStore)
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
			Accounts:       accountService,
			Bankroll:       bankrollService,
			Rooms:          roomService,
			Tables:         tableManager,
			Chat:           chatService,
			History:        historyStore,
			AllowedOrigins: appConfig.AllowedOrigins,
			Readiness: func(ctx context.Context) error {
				if database == nil {
					return nil
				}
				return database.PingContext(ctx)
			},
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
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
