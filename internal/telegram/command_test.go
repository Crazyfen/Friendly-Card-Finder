package telegram

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

// fakeDomain records what a command asked the deckbox module for.
type fakeDomain struct {
	searchResult deckbox.SearchResult
	searchErr    error
	searched     []string
	searchScope  deckbox.Scope
	searchCalls  int

	registered   deckbox.Registration
	registerErr  error
	registerCall int

	suggestResult deckbox.SuggestResult
	suggested     []string
}

func (f *fakeDomain) Search(ctx context.Context, cardNames []string, scope deckbox.Scope) (deckbox.SearchResult, error) {
	f.searchCalls++
	f.searched = cardNames
	f.searchScope = scope
	return f.searchResult, f.searchErr
}

func (f *fakeDomain) Register(ctx context.Context, r deckbox.Registration) error {
	f.registerCall++
	f.registered = r
	return f.registerErr
}

func (f *fakeDomain) Suggest(ctx context.Context, logins []string) deckbox.SuggestResult {
	f.suggested = logins
	return f.suggestResult
}

func newTestCommands(dbx Domain) *Commands {
	return NewCommands(slog.New(slog.NewTextHandler(io.Discard, nil)), dbx)
}

const lang = "en"

func req(text string) Request {
	return Request{Text: text, Lang: lang, UserID: 7, Username: "petya", ChatID: 100, MessageID: 5}
}

// --- Search and Sell ---

func TestSearchSplitsInputIntoCardNames(t *testing.T) {
	// One card per line, blanks and surrounding whitespace dropped. This is the
	// only place that rule lives, and until now no test could reach it.
	dbx := &fakeDomain{}
	newTestCommands(dbx).Search(context.Background(), req("  Lightning Bolt \n\n\tShock\n   \nRagavan\n"))

	want := []string{"Lightning Bolt", "Shock", "Ragavan"}
	if !reflect.DeepEqual(dbx.searched, want) {
		t.Errorf("searched %q, want %q", dbx.searched, want)
	}
	if dbx.searchScope != deckbox.ScopeTradelist {
		t.Errorf("plain text must search tradelists, got %q", dbx.searchScope)
	}
}

func TestSearchOfNothingAsksNothing(t *testing.T) {
	dbx := &fakeDomain{}
	replies := newTestCommands(dbx).Search(context.Background(), req("  \n\t\n "))

	if len(replies) != 0 {
		t.Errorf("blank input must produce no reply, got %+v", replies)
	}
	if dbx.searchCalls != 0 {
		t.Error("blank input must not reach the deckbox module")
	}
}

func TestSearchFailureIsSilent(t *testing.T) {
	// A failed search is logged, not reported: the alternative is a wall of Go
	// error text in a chat.
	dbx := &fakeDomain{searchErr: errors.New("database is locked")}
	replies := newTestCommands(dbx).Search(context.Background(), req("Shock"))

	if len(replies) != 0 {
		t.Errorf("a failed search must produce no reply, got %+v", replies)
	}
}

func TestSearchSendsNotFoundSeparately(t *testing.T) {
	// The not-found list is its own message so the ranked results are not pushed
	// off the top of a long reply.
	dbx := &fakeDomain{searchResult: deckbox.SearchResult{
		SearchQueries: []string{"Shock", "Ragavan"},
		Aggregates: []deckbox.UserSearchAggregate{{
			ListId: 1, DeckboxLogin: "masha",
			FoundCards: map[string]int16{"Shock": 2}, UniqueCount: 1, TotalQuantity: 2,
		}},
		NotFound: []string{"Ragavan"},
	}}

	replies := newTestCommands(dbx).Search(context.Background(), req("Shock\nRagavan"))

	if len(replies) != 2 {
		t.Fatalf("expected results plus a not-found message, got %d: %+v", len(replies), replies)
	}
	if !strings.Contains(replies[1].Text, "Ragavan") {
		t.Errorf("second reply should name the missing card, got %q", replies[1].Text)
	}
	for i, r := range replies {
		if !r.HTML || !r.ReplyTo {
			t.Errorf("reply %d should be an HTML reply to the query, got %+v", i, r)
		}
	}
}

func TestSellSearchesWishlists(t *testing.T) {
	dbx := &fakeDomain{}
	newTestCommands(dbx).Sell(context.Background(), req("Shock"))

	if dbx.searchScope != deckbox.ScopeWishlist {
		t.Errorf("/sell must search wishlists, got %q", dbx.searchScope)
	}
}

func TestSellWithoutArgumentExplainsItself(t *testing.T) {
	// The default handler stays silent on empty input; /sell has to answer,
	// because the user typed a command and got nothing back.
	dbx := &fakeDomain{}
	replies := newTestCommands(dbx).Sell(context.Background(), req("   "))

	if len(replies) != 1 || replies[0].Text != i18n.T(lang, "sell.no_argument") {
		t.Fatalf("expected the no-argument message, got %+v", replies)
	}
	if dbx.searchCalls != 0 {
		t.Error("an empty /sell must not reach the deckbox module")
	}
}

// --- Register ---

func TestRegisterPassesTheSenderThrough(t *testing.T) {
	dbx := &fakeDomain{}
	newTestCommands(dbx).Register(context.Background(), req(" petya_db "))

	want := deckbox.Registration{TelegramID: 7, TelegramUsername: "petya", DeckboxLogin: "petya_db"}
	if dbx.registered != want {
		t.Errorf("registered %+v, want %+v", dbx.registered, want)
	}
}

func TestRegisterWithoutLogin(t *testing.T) {
	dbx := &fakeDomain{}
	replies := newTestCommands(dbx).Register(context.Background(), req(""))

	if len(replies) != 1 || replies[0].Text != i18n.T(lang, "deckbox.register_no_argument") {
		t.Fatalf("expected the no-argument message, got %+v", replies)
	}
	if dbx.registerCall != 0 {
		t.Error("an empty /deckbox must not register anything")
	}
}

func TestRegisterFailureIsReported(t *testing.T) {
	dbx := &fakeDomain{registerErr: errors.New("duplicate user")}
	replies := newTestCommands(dbx).Register(context.Background(), req("petya_db"))

	if len(replies) != 1 || replies[0].Text != i18n.T(lang, "deckbox.register_error") {
		t.Fatalf("expected the error message, got %+v", replies)
	}
}

// --- Suggest: the two-phase command ---

func TestSuggestAcknowledgesThenSummarises(t *testing.T) {
	dbx := &fakeDomain{suggestResult: deckbox.SuggestResult{
		ProcessedCount: 2, SkippedCount: 1, Errors: []string{"masha: not found"},
	}}

	ack, run := newTestCommands(dbx).Suggest(req("petya\nmasha\nvasya"))

	if ack.Text == "" {
		t.Fatal("a bulk refresh must be acknowledged before it runs")
	}
	if run == nil {
		t.Fatal("expected work to run")
	}

	summary := run(context.Background())
	if !reflect.DeepEqual(dbx.suggested, []string{"petya", "masha", "vasya"}) {
		t.Errorf("suggested %q, want one login per line", dbx.suggested)
	}
	if !strings.Contains(summary.Text, "masha: not found") {
		t.Errorf("the summary should carry the errors, got %q", summary.Text)
	}
	if !summary.ReplyTo {
		t.Error("a summary arriving minutes later must quote the request")
	}
}

func TestSuggestWithoutLoginsRunsNothing(t *testing.T) {
	dbx := &fakeDomain{}
	ack, run := newTestCommands(dbx).Suggest(req(" "))

	if ack.Text != i18n.T(lang, "suggest.no_argument") {
		t.Errorf("expected the no-argument message, got %q", ack.Text)
	}
	if run != nil {
		t.Error("an empty /suggestdeckbox must not schedule work")
	}
}

// --- NewRequest ---

func TestNewRequestStripsTheCommand(t *testing.T) {
	tests := []struct {
		name    string
		message *models.Message
		want    string
	}{
		{
			name:    "plain message keeps its text",
			message: &models.Message{Text: "Lightning Bolt"},
			want:    "Lightning Bolt",
		},
		{
			name: "command argument survives the prefix",
			message: &models.Message{
				Text:     "/deckbox petya_db",
				Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Length: 8}},
			},
			want: "petya_db",
		},
		{
			name: "command with no argument is empty, not the command",
			message: &models.Message{
				Text:     "/sell",
				Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Length: 5}},
			},
			want: "",
		},
		{
			name: "a leading non-command entity is not stripped",
			message: &models.Message{
				Text:     "Sméagol, Helpful Guide",
				Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBold, Length: 7}},
			},
			want: "Sméagol, Helpful Guide",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewRequest(tt.message).Text; got != tt.want {
				t.Errorf("Text = %q, want %q", got, tt.want)
			}
		})
	}
}
