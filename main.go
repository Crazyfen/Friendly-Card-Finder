package main

import (
	"FriendlyCardFinder/internal/config"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/storage/sqlite"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type contextKey string

const (
	logKey     contextKey = "log"
	storageKey contextKey = "storage"
	scraperKey contextKey = "scraper"
	configKey  contextKey = "config"
	envLocal              = "local"
	envDev                = "dev"
	envProd               = "prod"
)

func main() {
	cfg := config.MustLoad()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log := setupLogger(cfg.Env)
	log = log.With(slog.String("env", cfg.Env))

	storage, err := sqlite.New(cfg.StoragePath)
	if err != nil {
		log.Error("failed to initialize storage", sl.Err(err))
		os.Exit(1)
	}
	defer func() {
		if err := storage.Close(); err != nil {
			log.Error("error closing storage", sl.Err(err))
		}
	}()

	scraper := deckbox.NewScraper(log, cfg.DeckboxSessionCookie)

	ctx = context.WithValue(ctx, logKey, log)
	ctx = context.WithValue(ctx, storageKey, storage)
	ctx = context.WithValue(ctx, scraperKey, scraper)
	ctx = context.WithValue(ctx, configKey, cfg)

	opts := []bot.Option{
		bot.WithDefaultHandler(defaultHandler),
	}

	b, err := bot.New(cfg.BotToken, opts...)
	if err != nil {
		log.Error("failed to create bot", sl.Err(err))
		os.Exit(1)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, startHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "sell", bot.MatchTypeCommandStartOnly, sellHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "deckbox", bot.MatchTypeCommandStartOnly, deckboxHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "suggestdeckbox", bot.MatchTypeCommandStartOnly, suggestdeckboxHandler)

	log.Info("starting bot")
	b.Start(ctx)
}

func setupLogger(env string) *slog.Logger {
	var log *slog.Logger

	switch env {
	case envLocal:
		fallthrough
	case envDev:
		log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	case envProd:
		log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	return log
}

func defaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.defaultHandler"
	log := ctx.Value(logKey).(*slog.Logger)
	log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID))).Info("handling default message")

	// Refresh stale user lists before searching
	config := ctx.Value(configKey).(*config.Config)
	deckbox.RefreshStaleUserLists(ctx, log, ctx.Value(storageKey).(deckbox.DeckboxSaver), ctx.Value(scraperKey).(*deckbox.Scraper), config.CardListRefreshHours)

	// Detect language from user
	lang := i18n.DetectLang(update.Message.From.LanguageCode)

	for line := range strings.SplitSeq(update.Message.Text, "\n") {
		line = strings.TrimSpace(line)
		log.Info("card to search", slog.String("line", line))

		result, err := deckbox.SearchCard(ctx, log, ctx.Value(storageKey).(deckbox.DeckboxSaver), line, deckbox.ScopeTradelist)
		if err != nil {
			log.Error("failed to search card", sl.Err(err))
			continue
		}

		message := result.FormatForTelegram(ctx, ctx.Value(storageKey).(deckbox.DeckboxSaver), lang, deckbox.ScopeTradelist)
		log.Info("sending card search response", slog.String("message", message))

		if len(message) > 4096 {
			parts := splitMessage(message, 4096)
			partsReplyTo := []int{int(update.Message.ID)}
			for i, p := range parts {
				reply, err := b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID:    update.Message.Chat.ID,
					Text:      p,
					ParseMode: models.ParseModeHTML,
					ReplyParameters: &models.ReplyParameters{
						MessageID: partsReplyTo[i],
					},
				})
				if err != nil {
					log.Error("failed to send card search response part", sl.Err(err), slog.String("part:", p))
				}
				partsReplyTo = append(partsReplyTo, int(reply.ID))
			}
			continue
		} else {

			_, err = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:    update.Message.Chat.ID,
				Text:      message,
				ParseMode: models.ParseModeHTML,
				ReplyParameters: &models.ReplyParameters{
					MessageID: int(update.Message.ID),
				},
			})
			if err != nil {
				log.Error("failed to send card search response", sl.Err(err))
			}
		}
	}
}

func startHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.startHandler"
	log := ctx.Value(logKey).(*slog.Logger)
	log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID))).Info("handling /start command")

	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	log.With(slog.String("LanguageCode", update.Message.From.LanguageCode), slog.String("detected_lang", lang)).Debug("detected user language")

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      i18n.T(lang, "start.welcome"),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		ctx.Value(logKey).(*slog.Logger).Error("failed to send /start response", sl.Err(err))
	}
}

func deckboxHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.deckboxHandler"
	log := ctx.Value(logKey).(*slog.Logger)
	log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID))).Info("handling /deckbox command")

	scraper := ctx.Value(scraperKey).(*deckbox.Scraper)
	lang := i18n.DetectLang(update.Message.From.LanguageCode)
	response := deckbox.NewUser(ctx, log, ctx.Value(storageKey).(deckbox.DeckboxSaver), scraper, update.Message, lang)

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   response,
	})
	if err != nil {
		log.Error("failed to send /deckbox response", sl.Err(err))
	}
}

func suggestdeckboxHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.suggestdeckboxHandler"
	log := ctx.Value(logKey).(*slog.Logger)
	log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID))).Info("handling /suggestdeckbox command")

	cfg := ctx.Value(configKey).(*config.Config)
	scraper := ctx.Value(scraperKey).(*deckbox.Scraper)
	storage := ctx.Value(storageKey).(deckbox.DeckboxSaver)

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

	// Parse logins (newline separated)
	logins := strings.Split(argument, "\n")

	// Spawn async processing
	go func() {
		result := deckbox.SuggestDeckbox(ctx, log, storage, scraper, logins, cfg.FreshnessTimeLimitHours)

		// Format response
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

	// Send immediate acknowledgement
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf(i18n.T(lang, "suggest.ack"), len(logins)),
	})
	if err != nil {
		log.Error("failed to send acknowledgement", sl.Err(err))
	}
}

func sellHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	const op = "handlers.sellHandler"
	log := ctx.Value(logKey).(*slog.Logger)
	log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(update.Message.ID))).Info("handling /sell command")

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

	// Refresh stale user lists before searching
	config := ctx.Value(configKey).(*config.Config)
	deckbox.RefreshStaleUserLists(ctx, log, ctx.Value(storageKey).(deckbox.DeckboxSaver), ctx.Value(scraperKey).(*deckbox.Scraper), config.CardListRefreshHours)

	for l := range strings.SplitSeq(argument, "\n") {
		line := strings.TrimSpace(l)
		if line == "" {
			continue
		}

		result, err := deckbox.SearchCard(ctx, log, ctx.Value(storageKey).(deckbox.DeckboxSaver), line, deckbox.ScopeWishlist)
		if err != nil {
			log.Error("failed to search card", sl.Err(err))
			continue
		}

		message := result.FormatForTelegram(ctx, ctx.Value(storageKey).(deckbox.DeckboxSaver), lang, deckbox.ScopeWishlist)

		if len(message) > 4096 {
			parts := splitMessage(message, 4096)
			partsReplyTo := []int{int(update.Message.ID)}
			for i, p := range parts {
				reply, err := b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID:    update.Message.Chat.ID,
					Text:      p,
					ParseMode: models.ParseModeHTML,
					ReplyParameters: &models.ReplyParameters{
						MessageID: partsReplyTo[i],
					},
				})
				if err != nil {
					log.Error("failed to send sell response part", sl.Err(err), slog.String("part", p))
				}
				partsReplyTo = append(partsReplyTo, int(reply.ID))
			}
			continue
		} else {
			_, err = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:    update.Message.Chat.ID,
				Text:      message,
				ParseMode: models.ParseModeHTML,
				ReplyParameters: &models.ReplyParameters{
					MessageID: int(update.Message.ID),
				},
			})
			if err != nil {
				log.Error("failed to send sell response", sl.Err(err))
			}
		}
	}
}

// splitMessage splits s into chunks not exceeding limit bytes,
// preferring to cut at the nearest newline before the limit.
// Preserves newlines in the chunk before the cut point when possible.
// Handles multi-byte UTF-8 characters correctly by respecting rune boundaries.
// If the final segment exceeds the limit, it is recursively split.
func splitMessage(s string, limit int) []string {
	if len(s) <= limit {
		return []string{s}
	}
	var parts []string
	start := 0
	lastNewline := -1

	for idx, r := range s {
		// Record newline position before checking limit
		if r == '\n' {
			lastNewline = idx
		}

		// Check if we've reached or exceeded the limit
		if idx-start >= limit {
			if lastNewline >= start && lastNewline+1-start <= limit {
				// Include newline in this chunk (if it doesn't exceed limit)
				parts = append(parts, s[start:lastNewline+1])
				start = lastNewline + 1
			} else if lastNewline >= start {
				// Newline exists but including it would exceed limit, cut before it
				parts = append(parts, s[start:lastNewline])
				start = lastNewline
			} else {
				// No newline, cut at current position (start of rune that exceeds limit)
				parts = append(parts, s[start:idx])
				start = idx
			}
			lastNewline = -1
		}
	}

	// Handle remaining portion, recursively split if still too large
	if start < len(s) {
		remaining := s[start:]
		if len(remaining) > limit {
			parts = append(parts, splitMessage(remaining, limit)...)
		} else {
			parts = append(parts, remaining)
		}
	}

	return parts
}
