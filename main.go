package main

import (
	"FriendlyCardFinder/internal/config"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/lib/tgutil"
	"FriendlyCardFinder/internal/storage/sqlite"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type App struct {
	log     *slog.Logger
	storage deckbox.DeckboxSaver
	scraper *deckbox.Scraper
	cfg     *config.Config
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

	app := &App{
		log:     log,
		storage: storage,
		scraper: deckbox.NewScraper(log, cfg.DeckboxSessionCookie),
		cfg:     cfg,
	}

	// Background refresh: runs immediately at startup then on a ticker so searches
	// are never blocked waiting for stale lists to refresh.
	go func() {
		deckbox.RefreshStaleUserLists(ctx, app.log, app.storage, app.scraper, app.cfg.CardListRefreshHours)
		ticker := time.NewTicker(time.Duration(30) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				deckbox.RefreshStaleUserLists(ctx, app.log, app.storage, app.scraper, app.cfg.CardListRefreshHours)
			case <-ctx.Done():
				return
			}
		}
	}()

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

	for line := range strings.SplitSeq(update.Message.Text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		log.Info("card to search", slog.String("line", line))

		result, err := deckbox.SearchCard(ctx, log, a.storage, line, deckbox.ScopeTradelist)
		if err != nil {
			log.Error("failed to search card", sl.Err(err))
			continue
		}

		message := result.FormatForTelegram(lang, deckbox.ScopeTradelist)
		log.Info("sending card search response", slog.String("message", message))
		sendHTMLReply(ctx, b, update.Message.Chat.ID, int(update.Message.ID), message, log)
	}
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
	response := deckbox.NewUser(ctx, log, a.storage, a.scraper, update.Message, lang)

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   response,
	})
	if err != nil {
		log.Error("failed to send /deckbox response", sl.Err(err))
	}
}

func (a *App) suggestdeckboxHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.suggestdeckboxHandler"
	log := a.log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID)))
	log.Info("handling /suggestdeckbox command")

	argument := deckbox.NewCommandArguments(update.Message)
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
		result := deckbox.SuggestDeckbox(ctx, log, a.storage, a.scraper, logins, a.cfg.FreshnessTimeLimitHours)

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

	argument := deckbox.NewCommandArguments(update.Message)
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

	for l := range strings.SplitSeq(argument, "\n") {
		line := strings.TrimSpace(l)
		if line == "" {
			continue
		}

		result, err := deckbox.SearchCard(ctx, log, a.storage, line, deckbox.ScopeWishlist)
		if err != nil {
			log.Error("failed to search card", sl.Err(err))
			continue
		}

		message := result.FormatForTelegram(lang, deckbox.ScopeWishlist)
		sendHTMLReply(ctx, b, update.Message.Chat.ID, int(update.Message.ID), message, log)
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
