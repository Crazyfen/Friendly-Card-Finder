package main

import (
	"FriendlyCardFinder/internal/admin"
	"FriendlyCardFinder/internal/chat"
	"FriendlyCardFinder/internal/config"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/discord"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/storage/sqlite"
	"FriendlyCardFinder/internal/telegram"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"time"

	"net/http"
	_ "net/http/pprof" // registers /debug/pprof handlers on DefaultServeMux
)

const (
	envLocal = "local"
	envDev   = "dev"
	envProd  = "prod"
)

func main() {
	cfg := config.MustLoad()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log := setupLogger(cfg.Env)
	log = log.With(slog.String("env", cfg.Env))

	if cfg.Env == envLocal || cfg.Env == envDev {
		// Enable block/mutex sampling so those profiles return data
		// (off by default). Hot enough only matters under load.
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		go func() {
			log.Info("pprof listening on localhost:6060")
			// localhost-only so it's never exposed on the VPS
			if err := http.ListenAndServe("localhost:6060", nil); err != nil {
				log.Error("pprof server failed", sl.Err(err))
			}
		}()
	}

	storage, err := sqlite.New(cfg.StoragePath, log, cfg.CardListBatchSize)
	if err != nil {
		log.Error("failed to initialize storage", sl.Err(err))
		os.Exit(1)
	}
	defer func() {
		if err := storage.Close(); err != nil {
			log.Error("error closing storage", sl.Err(err))
		}
	}()

	scraper := deckbox.NewScraper(log, deckbox.ScraperAuth{
		Login:          cfg.DeckboxLogin,
		Password:       cfg.DeckboxPassword,
		CookieOverride: cfg.DeckboxSessionCookie,
		CookiePath:     cfg.DeckboxCookiePath,
	})

	dbx := deckbox.New(log, storage, storage, scraper, cfg.CardListRefreshHours, cfg.FreshnessTimeLimitHours)

	// Background refresh: runs immediately at startup then on a ticker so searches
	// are never blocked waiting for stale lists to refresh.
	go func() {
		dbx.RefreshStale(ctx)
		ticker := time.NewTicker(time.Duration(30) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				dbx.RefreshStale(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Admin Panel. It refuses to start without a password rather than exposing a
	// purge endpoint openly, so a missing ADMIN_PASSWORD disables it loudly.
	if panel, err := admin.New(log, dbx, admin.Config{
		Addr:     cfg.AdminAddr,
		User:     cfg.AdminUser,
		Password: cfg.AdminPassword,
	}); err != nil {
		log.Warn("admin panel disabled", sl.Err(err))
	} else {
		go func() {
			if err := panel.ListenAndServe(ctx); err != nil {
				log.Error("admin panel failed", sl.Err(err))
			}
		}()
	}

	cmd := chat.NewCommands(log, dbx)

	// Discord is optional: without a token only Telegram runs. A Discord failure
	// is logged and leaves the Telegram bot up.
	if cfg.DiscordToken == "" {
		log.Warn("discord disabled")
	} else if dc, err := discord.New(log, cmd, cfg.DiscordToken); err != nil {
		log.Error("failed to create discord bot", sl.Err(err))
	} else {
		go func() {
			if err := dc.Run(ctx); err != nil {
				log.Error("failed to run discord bot", sl.Err(err))
			}
		}()
	}

	if err := telegram.New(log, cmd).Run(ctx, cfg.BotToken); err != nil {
		log.Error("failed to run telegram bot", sl.Err(err))
		os.Exit(1)
	}
}

func setupLogger(env string) *slog.Logger {
	switch env {
	case envLocal, envDev:
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	default:
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
}
