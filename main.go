package main

import (
	"FriendlyCardFinder/internal/admin"
	"FriendlyCardFinder/internal/config"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/lib/tgutil"
	"FriendlyCardFinder/internal/storage/sqlite"
	"FriendlyCardFinder/internal/telegram"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"net/http"
	_ "net/http/pprof" // registers /debug/pprof handlers on DefaultServeMux

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type App struct {
	log *slog.Logger
	dbx *deckbox.Deckbox
}

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

	storage, err := sqlite.New(cfg.StoragePath, log)
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

	app := &App{
		log: log,
		dbx: deckbox.New(log, storage, scraper, cfg.CardListRefreshHours, cfg.FreshnessTimeLimitHours),
	}

	// Background refresh: runs immediately at startup then on a ticker so searches
	// are never blocked waiting for stale lists to refresh.
	go func() {
		app.dbx.RefreshStale(ctx)
		ticker := time.NewTicker(time.Duration(30) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				app.dbx.RefreshStale(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Admin Panel. It refuses to start without a password rather than exposing a
	// purge endpoint openly, so a missing ADMIN_PASSWORD disables it loudly.
	if panel, err := admin.New(log, app.dbx, admin.Config{
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
		bot.WithDefaultHandler(app.defaultHandler),
	}

	b, err := bot.New(cfg.BotToken, opts...)
	if err != nil {
		log.Error("failed to create bot", sl.Err(err))
		os.Exit(1)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, app.startHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "sell", bot.MatchTypeCommandStartOnly, app.sellHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "deckbox", bot.MatchTypeCommandStartOnly, app.deckboxHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "suggestdeckbox", bot.MatchTypeCommandStartOnly, app.suggestdeckboxHandler)

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

func (a *App) defaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.defaultHandler"
	log := a.log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID)))
	log.Info("handling default message")

	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	a.searchAndReply(ctx, b, log, lang, update.Message.Text, update.Message.Chat.ID, int(update.Message.ID), deckbox.ScopeTradelist)
}

func (a *App) startHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.startHandler"
	log := a.log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID)))
	log.Info("handling /start command")

	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	log.With(slog.String("LanguageCode", update.Message.From.LanguageCode), slog.String("detected_lang", lang)).Debug("detected user language")

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      i18n.T(lang, "start.welcome"),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		log.Error("failed to send /start response", sl.Err(err))
	}
}

func (a *App) deckboxHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.deckboxHandler"
	log := a.log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID)))
	log.Info("handling /deckbox command")

	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	response := a.register(ctx, log, update.Message, lang)

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   response,
	})
	if err != nil {
		log.Error("failed to send /deckbox response", sl.Err(err))
	}
}

// register turns a /deckbox message into a Registration and reports the outcome
// in the user's language.
func (a *App) register(ctx context.Context, log *slog.Logger, message *models.Message, lang string) string {
	login := telegram.CommandArgument(message)
	if login == "" {
		log.Info("forgotten deckbox login")
		return i18n.T(lang, "deckbox.register_no_argument")
	}

	err := a.dbx.Register(ctx, deckbox.Registration{
		TelegramID:       message.From.ID,
		TelegramUsername: message.From.Username,
		DeckboxLogin:     login,
	})
	if err != nil {
		return i18n.T(lang, "deckbox.register_error")
	}

	return fmt.Sprintf(i18n.T(lang, "deckbox.register_success"), message.From.FirstName, message.From.LastName)
}

func (a *App) suggestdeckboxHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.suggestdeckboxHandler"
	log := a.log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID)))
	log.Info("handling /suggestdeckbox command")

	argument := telegram.CommandArgument(update.Message)
	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	if argument == "" {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   i18n.T(lang, "suggest.no_argument"),
		})
		if err != nil {
			log.Error("failed to send response", sl.Err(err))
		}
		return
	}

	logins := strings.Split(argument, "\n")

	go func() {
		result := a.dbx.Suggest(ctx, logins)

		var response strings.Builder
		response.WriteString(fmt.Sprintf(i18n.T(lang, "suggest.summary"),
			result.ProcessedCount, result.SkippedCount))

		if len(result.Errors) > 0 {
			response.WriteString("\n\n")
			response.WriteString(i18n.T(lang, "suggest.errors_header"))
			for _, err := range result.Errors {
				response.WriteString(fmt.Sprintf("• %s\n", err))
			}
		}

		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   response.String(),
			ReplyParameters: &models.ReplyParameters{
				MessageID: int(update.Message.ID),
			},
		})
		if err != nil {
			log.Error("failed to send suggest response", sl.Err(err))
		}
	}()

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf(i18n.T(lang, "suggest.ack"), len(logins)),
	})
	if err != nil {
		log.Error("failed to send acknowledgement", sl.Err(err))
	}
}

func (a *App) sellHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.sellHandler"
	log := a.log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID)))
	log.Info("handling /sell command")

	argument := telegram.CommandArgument(update.Message)
	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	if argument == "" {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   i18n.T(lang, "sell.no_argument"),
		})
		if err != nil {
			log.Error("failed to send response", sl.Err(err))
		}
		return
	}

	a.searchAndReply(ctx, b, log, lang, argument, update.Message.Chat.ID, int(update.Message.ID), deckbox.ScopeWishlist)
}

// searchAndReply parses one-card-per-line input, searches, and sends the
// rendered result (plus a not-found message when one is produced).
func (a *App) searchAndReply(ctx context.Context, b *bot.Bot, log *slog.Logger, lang, text string, chatID int64, replyToID int, scope string) {
	var cards []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			cards = append(cards, line)
		}
	}
	if len(cards) == 0 {
		return
	}

	result, err := a.dbx.Search(ctx, cards, scope)
	if err != nil {
		log.Error("failed to search cards", sl.Err(err))
		return
	}

	mainMsg, notFoundMsg := telegram.RenderSearch(result, lang, scope)
	sendHTMLReply(ctx, b, chatID, replyToID, mainMsg, log)
	if notFoundMsg != "" {
		sendHTMLReply(ctx, b, chatID, replyToID, notFoundMsg, log)
	}
}

// sendHTMLReply sends an HTML-formatted message, splitting it into chained reply
// parts if it exceeds Telegram's 4096-byte limit.
func sendHTMLReply(ctx context.Context, b *bot.Bot, chatID int64, replyToID int, message string, log *slog.Logger) {
	parts := tgutil.SplitMessage(message, 4096)
	replyTo := replyToID
	for _, p := range parts {
		reply, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      p,
			ParseMode: models.ParseModeHTML,
			ReplyParameters: &models.ReplyParameters{
				MessageID: replyTo,
			},
		})
		if err != nil {
			log.Error("failed to send response", sl.Err(err))
			return
		}
		replyTo = int(reply.ID)
	}
}
