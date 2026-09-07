package main

import (
	"FriendlyCardFinder/internal/admin"
	"FriendlyCardFinder/internal/config"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/lib/tgutil"
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

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// App is the Telegram adapter: it maps updates to telegram.Requests and the
// Replies that come back to SendMessage calls. It decides nothing a user can
// see — that lives in telegram.Commands, where tests can reach it.
type App struct {
	log *slog.Logger
	cmd *telegram.Commands
}

const (
	envLocal = "local"
	envDev   = "dev"
	envProd  = "prod"
)

// telegramMessageLimit is the largest message Telegram accepts, in bytes.
const telegramMessageLimit = 4096

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
	app := &App{log: log, cmd: telegram.NewCommands(log, dbx)}

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

	opts := []bot.Option{
		bot.WithDefaultHandler(app.handle("search", app.cmd.Search)),
	}

	b, err := bot.New(cfg.BotToken, opts...)
	if err != nil {
		log.Error("failed to create bot", sl.Err(err))
		os.Exit(1)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, app.handle("start", app.cmd.Start))
	b.RegisterHandler(bot.HandlerTypeMessageText, "sell", bot.MatchTypeCommandStartOnly, app.handle("sell", app.cmd.Sell))
	b.RegisterHandler(bot.HandlerTypeMessageText, "deckbox", bot.MatchTypeCommandStartOnly, app.handle("deckbox", app.cmd.Register))
	b.RegisterHandler(bot.HandlerTypeMessageText, "suggestdeckbox", bot.MatchTypeCommandStartOnly, app.suggestHandler)

	log.Info("starting bot")
	b.Start(ctx)
}

func setupLogger(env string) *slog.Logger {
	switch env {
	case envLocal, envDev:
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	default:
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
}

// handle adapts one command to the Telegram SDK. Every command goes through it,
// so the send-and-log-error path is written once.
func (a *App) handle(name string, cmd func(context.Context, telegram.Request) []telegram.Reply) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil {
			return
		}
		log := a.log.With(slog.String("operation", "handlers."+name), slog.Int("message_id", update.Message.ID))
		log.Info("handling message")

		req := telegram.NewRequest(update.Message)
		a.send(ctx, b, log, req, cmd(ctx, req)...)
	}
}

// suggestHandler is the one command that answers twice: an acknowledgement now,
// and the summary when the bulk Refresh finishes.
func (a *App) suggestHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	log := a.log.With(slog.String("operation", "handlers.suggestdeckbox"), slog.Int("message_id", update.Message.ID))
	log.Info("handling message")

	req := telegram.NewRequest(update.Message)
	ack, run := a.cmd.Suggest(req)
	a.send(ctx, b, log, req, ack)
	if run == nil {
		return
	}

	go a.send(ctx, b, log, req, run(ctx))
}

// send delivers replies in order, splitting any that exceed Telegram's message
// limit and chaining the parts so a long answer reads as one thread.
func (a *App) send(ctx context.Context, b *bot.Bot, log *slog.Logger, req telegram.Request, replies ...telegram.Reply) {
	for _, r := range replies {
		if r.Text == "" {
			continue
		}

		replyTo := 0
		if r.ReplyTo {
			replyTo = req.MessageID
		}

		for _, part := range tgutil.SplitMessage(r.Text, telegramMessageLimit) {
			params := &bot.SendMessageParams{ChatID: req.ChatID, Text: part}
			if r.HTML {
				params.ParseMode = models.ParseModeHTML
			}
			if replyTo != 0 {
				params.ReplyParameters = &models.ReplyParameters{MessageID: replyTo}
			}

			sent, err := b.SendMessage(ctx, params)
			if err != nil {
				log.Error("failed to send response", sl.Err(err))
				break
			}
			replyTo = int(sent.ID)
		}
	}
}
