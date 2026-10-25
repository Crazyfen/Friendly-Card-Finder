package chat

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/message"
	"context"
	"log/slog"
	"strconv"
	"strings"
)

// Request is one incoming message reduced to what a command needs. Text is the
// message with any bot-command prefix stripped, so a command reads its argument
// and the default search reads the whole message from the same field.
type Request struct {
	Text      string
	Lang      string
	Platform  deckbox.Platform
	UserID    int64
	Username  string
	FirstName string
	LastName  string
}

// Reply is one message to send back. Commands return these instead of sending,
// so every branch they take is reachable from a test without a bot.
type Reply struct {
	Message message.Message
	ReplyTo bool // quote the message that triggered the command
	Private bool // only the requester should see it, where the Platform can do that
}

// Domain is what the commands need from the deckbox module. It is declared here,
// in the consumer, so a test drives every command with a fake instead of a
// database, a scraper and a chat connection.
type Domain interface {
	Search(ctx context.Context, cardNames []string, scope deckbox.Scope) (deckbox.SearchResult, error)
	Register(ctx context.Context, r deckbox.Registration) error
	Suggest(ctx context.Context, logins []string) deckbox.SuggestResult
}

// Commands is the whole of the bot's conversational behaviour: which wording a
// message gets, what counts as a missing argument, how input is split into card
// names, and which Scope a command searches. Nothing above it decides anything,
// and nothing below it knows which Platform it is talking to.
type Commands struct {
	log *slog.Logger
	dbx Domain
}

func NewCommands(log *slog.Logger, dbx Domain) *Commands {
	return &Commands{log: log, dbx: dbx}
}

// Start greets a new Bot User. It takes a context it does not use so every
// command has the same shape for the caller to route.
func (c *Commands) Start(_ context.Context, req Request) []Reply {
	lang := req.Lang
	m := message.Message{message.Bold(i18n.T(lang, "start.title")), message.Text("\n\n")}
	m = append(m, i18n.Format(lang, "start.intro")...)

	// Only the wording of search, sell, suggest and the footer differs by Platform.
	plat := "telegram"
	searchArgs := []message.Span{message.Code(`"Shock"`)}
	sellArg := message.Code(i18n.T(lang, "start.sell.code"))
	if req.Platform == deckbox.PlatformDiscord {
		plat = "discord"
		searchArgs = []message.Span{message.Code("/search"), message.Code(`"Shock"`)}
		sellArg = message.Code("/sell")
	}
	sections := []struct {
		heading, body string
		args          []message.Span
	}{
		{"start.register.heading", "start.register.body", []message.Span{message.Code(i18n.T(lang, "start.register.code"))}},
		{"start.search.heading", "start." + plat + ".search.body", searchArgs},
		{"start.sell.heading", "start." + plat + ".sell.body", []message.Span{sellArg}},
		{"start.suggest.heading", "start." + plat + ".suggest.body", []message.Span{message.Code("/suggestdeckbox")}},
	}
	for _, s := range sections {
		m = append(m, message.Text("\n\n"), message.Bold(i18n.T(lang, s.heading)), message.Text("\n"))
		m = append(m, i18n.Format(lang, s.body, s.args...)...)
	}

	m = append(m, message.Text("\n\n"))
	m = append(m, i18n.Format(lang, "start."+plat+".footer")...)
	return []Reply{{Message: m, Private: true}}
}

// Search is the default handler: one card name per line, searched against
// tradelists.
func (c *Commands) Search(ctx context.Context, req Request) []Reply {
	return c.search(ctx, req, deckbox.ScopeTradelist)
}

// Sell searches wishlists — who wants the cards you are offering.
func (c *Commands) Sell(ctx context.Context, req Request) []Reply {
	if strings.TrimSpace(req.Text) == "" {
		return []Reply{{Message: i18n.Format(req.Lang, "sell.no_argument"), Private: true}}
	}
	return c.search(ctx, req, deckbox.ScopeWishlist)
}

func (c *Commands) search(ctx context.Context, req Request, scope deckbox.Scope) []Reply {
	const op = "chat.search"
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
	replies := []Reply{{Message: main, ReplyTo: true}}
	if notFound != nil {
		replies = append(replies, Reply{Message: notFound, ReplyTo: true})
	}
	return replies
}

// Register claims a Deckbox login for the sender.
func (c *Commands) Register(ctx context.Context, req Request) []Reply {
	const op = "chat.Register"
	log := c.log.With(slog.String("operation", op))

	login := strings.TrimSpace(req.Text)
	if login == "" {
		log.Info("forgotten deckbox login")
		return []Reply{{Message: i18n.Format(req.Lang, "deckbox.register_no_argument"), Private: true}}
	}

	if err := c.dbx.Register(ctx, deckbox.Registration{
		Platform:     req.Platform,
		UserID:       req.UserID,
		Handle:       req.Username,
		DeckboxLogin: login,
	}); err != nil {
		return []Reply{{Message: i18n.Format(req.Lang, "deckbox.register_error"), Private: true}}
	}

	return []Reply{{Message: i18n.Format(req.Lang, "deckbox.register_success",
		message.Text(req.FirstName), message.Text(req.LastName)), Private: true}}
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
		return Reply{Message: i18n.Format(req.Lang, "suggest.no_argument"), Private: true}, nil
	}

	logins := strings.Split(req.Text, "\n")
	ack := Reply{Message: i18n.Format(req.Lang, "suggest.ack", count(len(logins))), Private: true}

	return ack, func(ctx context.Context) Reply {
		result := c.dbx.Suggest(ctx, logins)

		summary := i18n.Format(req.Lang, "suggest.summary", count(result.ProcessedCount), count(result.SkippedCount))
		if len(result.Errors) > 0 {
			summary = append(summary, message.Text("\n\n"))
			summary = append(summary, i18n.Format(req.Lang, "suggest.errors_header")...)
			for _, e := range result.Errors {
				summary = append(summary, message.Text("• "+e+"\n"))
			}
		}
		return Reply{Message: summary, ReplyTo: true}
	}
}

// count is a number as the text span a translation's %s takes.
func count(n int) message.Span { return message.Text(strconv.Itoa(n)) }
