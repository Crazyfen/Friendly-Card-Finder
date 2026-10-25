// Package telegram is the Telegram adapter: it maps SDK updates to
// chat.Requests and the chat.Replies that come back to SendMessage calls, with
// each Message formatted as HTML. It decides nothing a user can see — that
// lives in chat.Commands, where tests can reach it.
package telegram

import (
	"FriendlyCardFinder/internal/chat"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/lib/tgutil"
	"context"
	"fmt"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// messageLimit is the largest message Telegram accepts, in bytes.
const messageLimit = 4096

// Adapter serves chat.Commands over the Telegram Bot API.
type Adapter struct {
	log *slog.Logger
	cmd *chat.Commands
}

func New(log *slog.Logger, cmd *chat.Commands) *Adapter {
	return &Adapter{log: log, cmd: cmd}
}

// Run connects to Telegram and serves updates until ctx ends.
func (a *Adapter) Run(ctx context.Context, token string) error {
	const op = "telegram.Run"
	log := a.log.With(slog.String("operation", op))

	b, err := bot.New(token, bot.WithDefaultHandler(a.handle("search", a.cmd.Search)))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, a.handle("start", a.cmd.Start))
	b.RegisterHandler(bot.HandlerTypeMessageText, "sell", bot.MatchTypeCommandStartOnly, a.handle("sell", a.cmd.Sell))
	b.RegisterHandler(bot.HandlerTypeMessageText, "deckbox", bot.MatchTypeCommandStartOnly, a.handle("deckbox", a.cmd.Register))
	b.RegisterHandler(bot.HandlerTypeMessageText, "suggestdeckbox", bot.MatchTypeCommandStartOnly, a.suggestHandler)

	log.Info("starting bot")
	b.Start(ctx)
	return nil
}

// NewRequest reduces a Telegram message to a Request.
func NewRequest(m *models.Message) chat.Request {
	text := m.Text
	if arg, isCommand := commandArgument(m); isCommand {
		text = arg
	}

	req := chat.Request{Text: text, Platform: deckbox.PlatformTelegram}
	if m.From != nil {
		req.Lang = i18n.DetectLang(m.From.LanguageCode)
		req.UserID = m.From.ID
		req.Username = m.From.Username
		req.FirstName = m.From.FirstName
		req.LastName = m.From.LastName
	}
	return req
}

// commandArgument reports whether the message opens with a bot command, and
// returns the text following it — empty when the command carries no argument.
func commandArgument(m *models.Message) (string, bool) {
	if len(m.Entities) == 0 {
		return "", false
	}
	entity := m.Entities[0]
	if entity.Type != models.MessageEntityTypeBotCommand {
		return "", false
	}
	if len(m.Text) == entity.Length {
		return "", true
	}
	return m.Text[entity.Length+1:], true
}

// handle adapts one command to the Telegram SDK. Every command goes through it,
// so the send-and-log-error path is written once.
func (a *Adapter) handle(name string, cmd func(context.Context, chat.Request) []chat.Reply) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil {
			return
		}
		log := a.log.With(slog.String("operation", "handlers."+name), slog.Int("message_id", update.Message.ID))
		log.Info("handling message")

		a.send(ctx, b, log, update.Message, cmd(ctx, NewRequest(update.Message))...)
	}
}

// suggestHandler is the one command that answers twice: an acknowledgement now,
// and the summary when the bulk Refresh finishes.
func (a *Adapter) suggestHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	log := a.log.With(slog.String("operation", "handlers.suggestdeckbox"), slog.Int("message_id", update.Message.ID))
	log.Info("handling message")

	ack, run := a.cmd.Suggest(NewRequest(update.Message))
	a.send(ctx, b, log, update.Message, ack)
	if run == nil {
		return
	}

	go a.send(ctx, b, log, update.Message, run(ctx))
}

// send delivers replies to the chat of m in order, splitting any that exceed
// Telegram's message limit and chaining the parts so a long answer reads as one
// thread. Every reply is HTML, which is safe because Format escapes all text.
// Private is ignored: Telegram has no reply only the requester sees.
func (a *Adapter) send(ctx context.Context, b *bot.Bot, log *slog.Logger, m *models.Message, replies ...chat.Reply) {
	for _, r := range replies {
		text := Format(r.Message)
		if text == "" {
			continue
		}

		replyTo := 0
		if r.ReplyTo {
			replyTo = m.ID
		}

		for _, part := range tgutil.SplitMessage(text, messageLimit) {
			params := &bot.SendMessageParams{ChatID: m.Chat.ID, Text: part, ParseMode: models.ParseModeHTML}
			if replyTo != 0 {
				params.ReplyParameters = &models.ReplyParameters{MessageID: replyTo}
			}

			sent, err := b.SendMessage(ctx, params)
			if err != nil {
				log.Error("failed to send response", sl.Err(err))
				break
			}
			replyTo = sent.ID
		}
	}
}
