package telegram

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot/models"
)

// Request is one incoming message reduced to what a command needs. Text is the
// message with any bot-command prefix stripped, so a command reads its argument
// and the default search reads the whole message from the same field.
type Request struct {
	Text      string
	Lang      string
	UserID    int64
	Username  string
	FirstName string
	LastName  string
	ChatID    int64
	MessageID int
}

// Reply is one message to send back. Commands return these instead of sending,
// so every branch they take is reachable from a test without a bot.
type Reply struct {
	Text    string
	HTML    bool // send with ParseMode HTML
	ReplyTo bool // quote the message that triggered the command
}

// Domain is what the commands need from the deckbox module. It is declared here,
// in the consumer, so a test drives every command with a fake instead of a
// database, a scraper and a Telegram connection.
type Domain interface {
	Search(ctx context.Context, cardNames []string, scope deckbox.Scope) (deckbox.SearchResult, error)
	Register(ctx context.Context, r deckbox.Registration) error
	Suggest(ctx context.Context, logins []string) deckbox.SuggestResult
}

// Commands is the whole of the bot's conversational behaviour: which wording a
// message gets, what counts as a missing argument, how input is split into card
// names, and which Scope a command searches. Nothing above it decides anything,
// and nothing below it knows about Telegram.
type Commands struct {
	log *slog.Logger
	dbx Domain
}

func NewCommands(log *slog.Logger, dbx Domain) *Commands {
	return &Commands{log: log, dbx: dbx}
}

// NewRequest reduces a Telegram message to a Request.
func NewRequest(m *models.Message) Request {
	text := m.Text
	if arg, isCommand := commandArgument(m); isCommand {
		text = arg
	}

	req := Request{
		Text:      text,
		ChatID:    m.Chat.ID,
		MessageID: int(m.ID),
	}
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

// Start greets a new Bot User. It takes a context it does not use so every
// command has the same shape for the caller to route.
func (c *Commands) Start(_ context.Context, req Request) []Reply {
	return []Reply{{Text: i18n.T(req.Lang, "start.welcome"), HTML: true}}
}

// Search is the default handler: one card name per line, searched against
// tradelists.
func (c *Commands) Search(ctx context.Context, req Request) []Reply {
	return c.search(ctx, req, deckbox.ScopeTradelist)
}

// Sell searches wishlists — who wants the cards you are offering.
func (c *Commands) Sell(ctx context.Context, req Request) []Reply {
	if strings.TrimSpace(req.Text) == "" {
		return []Reply{{Text: i18n.T(req.Lang, "sell.no_argument")}}
	}
	return c.search(ctx, req, deckbox.ScopeWishlist)
}

func (c *Commands) search(ctx context.Context, req Request, scope deckbox.Scope) []Reply {
	const op = "telegram.search"
	log := c.log.With(slog.String("operation", op), slog.String("scope", string(scope)))

	var cards []string
	for line := range strings.SplitSeq(req.Text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			cards = append(cards, line)
		}
	}
	if len(cards) == 0 {
		return nil
	}

	result, err := c.dbx.Search(ctx, cards, scope)
	if err != nil {
		log.Error("failed to search cards", sl.Err(err))
		return nil
	}

	main, notFound := RenderSearch(result, req.Lang, scope)
	replies := []Reply{{Text: main, HTML: true, ReplyTo: true}}
	if notFound != "" {
		replies = append(replies, Reply{Text: notFound, HTML: true, ReplyTo: true})
	}
	return replies
}

// Register claims a Deckbox login for the sender.
func (c *Commands) Register(ctx context.Context, req Request) []Reply {
	const op = "telegram.Register"
	log := c.log.With(slog.String("operation", op))

	login := strings.TrimSpace(req.Text)
	if login == "" {
		log.Info("forgotten deckbox login")
		return []Reply{{Text: i18n.T(req.Lang, "deckbox.register_no_argument")}}
	}

	if err := c.dbx.Register(ctx, deckbox.Registration{
		TelegramID:       req.UserID,
		TelegramUsername: req.Username,
		DeckboxLogin:     login,
	}); err != nil {
		return []Reply{{Text: i18n.T(req.Lang, "deckbox.register_error")}}
	}

	return []Reply{{Text: fmt.Sprintf(i18n.T(req.Lang, "deckbox.register_success"), req.FirstName, req.LastName)}}
}

// Suggest is the one command whose work outlives the message: a bulk Refresh
// takes minutes, so it acknowledges now and summarises later. It returns the
// acknowledgement and, when there is work to do, the function that produces the
// summary — the caller decides where that runs, and a test runs it inline.
//
// Do not "simplify" this into a goroutine inside Commands: that puts the slow
// half back where a test cannot wait for it, which is the whole reason the
// commands return values instead of sending.
func (c *Commands) Suggest(req Request) (Reply, func(context.Context) Reply) {
	if strings.TrimSpace(req.Text) == "" {
		return Reply{Text: i18n.T(req.Lang, "suggest.no_argument")}, nil
	}

	logins := strings.Split(req.Text, "\n")
	ack := Reply{Text: fmt.Sprintf(i18n.T(req.Lang, "suggest.ack"), len(logins))}

	return ack, func(ctx context.Context) Reply {
		result := c.dbx.Suggest(ctx, logins)

		var summary strings.Builder
		fmt.Fprintf(&summary, i18n.T(req.Lang, "suggest.summary"), result.ProcessedCount, result.SkippedCount)
		if len(result.Errors) > 0 {
			summary.WriteString("\n\n")
			summary.WriteString(i18n.T(req.Lang, "suggest.errors_header"))
			for _, e := range result.Errors {
				fmt.Fprintf(&summary, "• %s\n", e)
			}
		}
		return Reply{Text: summary.String(), ReplyTo: true}
	}
}
