package chat

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
	return Request{Text: text, Lang: lang, Platform: deckbox.PlatformTelegram, UserID: 7, Username: "petya"}
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
	if !strings.Contains(replies[1].Message.PlainText(), "Ragavan") {
		t.Errorf("second reply should name the missing card, got %q", replies[1].Message.PlainText())
	}
	for i, r := range replies {
		if !r.ReplyTo {
			t.Errorf("reply %d should quote the query, got %+v", i, r)
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

	if len(replies) != 1 || replies[0].Message.PlainText() != i18n.T(lang, "sell.no_argument") {
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

	want := deckbox.Registration{Platform: deckbox.PlatformTelegram, UserID: 7, Handle: "petya", DeckboxLogin: "petya_db"}
	if dbx.registered != want {
		t.Errorf("registered %+v, want %+v", dbx.registered, want)
	}
}

func TestRegisterUsesTheRequestPlatform(t *testing.T) {
	dbx := &fakeDomain{}
	r := req("alice_db")
	r.Platform = deckbox.PlatformDiscord
	newTestCommands(dbx).Register(context.Background(), r)

	if dbx.registered.Platform != deckbox.PlatformDiscord {
		t.Errorf("registered on %q, want the Request's Platform", dbx.registered.Platform)
	}
}

func TestRegisterSuccessNamesTheSender(t *testing.T) {
	r := req("petya_db")
	r.FirstName, r.LastName = "Petya", "Ivanov"
	replies := newTestCommands(&fakeDomain{}).Register(context.Background(), r)

	if len(replies) != 1 || replies[0].Message.PlainText() != "Saved your Deckbox nick, Petya Ivanov" {
		t.Fatalf("expected the success message, got %+v", replies)
	}
}

func TestRegisterWithoutLogin(t *testing.T) {
	dbx := &fakeDomain{}
	replies := newTestCommands(dbx).Register(context.Background(), req(""))

	if len(replies) != 1 || replies[0].Message.PlainText() != i18n.T(lang, "deckbox.register_no_argument") {
		t.Fatalf("expected the no-argument message, got %+v", replies)
	}
	if dbx.registerCall != 0 {
		t.Error("an empty /deckbox must not register anything")
	}
}

func TestRegisterFailureIsReported(t *testing.T) {
	dbx := &fakeDomain{registerErr: errors.New("duplicate user")}
	replies := newTestCommands(dbx).Register(context.Background(), req("petya_db"))

	if len(replies) != 1 || replies[0].Message.PlainText() != i18n.T(lang, "deckbox.register_error") {
		t.Fatalf("expected the error message, got %+v", replies)
	}
}

// --- Suggest: the two-phase command ---

func TestSuggestAcknowledgesThenSummarises(t *testing.T) {
	dbx := &fakeDomain{suggestResult: deckbox.SuggestResult{
		ProcessedCount: 2, SkippedCount: 1, Errors: []string{"masha: not found"},
	}}

	ack, run := newTestCommands(dbx).Suggest(req("petya\nmasha\nvasya"))

	if len(ack.Message) == 0 {
		t.Fatal("a bulk refresh must be acknowledged before it runs")
	}
	if run == nil {
		t.Fatal("expected work to run")
	}

	summary := run(context.Background())
	if !reflect.DeepEqual(dbx.suggested, []string{"petya", "masha", "vasya"}) {
		t.Errorf("suggested %q, want one login per line", dbx.suggested)
	}
	if !strings.Contains(summary.Message.PlainText(), "masha: not found") {
		t.Errorf("the summary should carry the errors, got %q", summary.Message.PlainText())
	}
	if !summary.ReplyTo {
		t.Error("a summary arriving minutes later must quote the request")
	}
}

func TestSuggestDropsBlankLines(t *testing.T) {
	dbx := &fakeDomain{}
	ack, run := newTestCommands(dbx).Suggest(req("petya\n\n  \nmasha\n"))
	run(context.Background())

	if !reflect.DeepEqual(dbx.suggested, []string{"petya", "masha"}) {
		t.Errorf("suggested %q, want the blank lines dropped", dbx.suggested)
	}
	if got := ack.Message.PlainText(); got != "Starting processing of 2 logins..." {
		t.Errorf("ack = %q, must not count blank lines", got)
	}
}

func TestRegisterInvalidLoginExplainsTheRule(t *testing.T) {
	dbx := &fakeDomain{registerErr: deckbox.ErrInvalidLogin}
	replies := newTestCommands(dbx).Register(context.Background(), req("@petya"))

	if len(replies) != 1 || !strings.HasPrefix(replies[0].Message.PlainText(), "@petya doesn't look like a Deckbox login") {
		t.Fatalf("expected the invalid-login message, got %+v", replies)
	}
}

func TestSuggestWithoutLoginsRunsNothing(t *testing.T) {
	dbx := &fakeDomain{}
	ack, run := newTestCommands(dbx).Suggest(req(" "))

	if ack.Message.PlainText() != i18n.T(lang, "suggest.no_argument") {
		t.Errorf("expected the no-argument message, got %q", ack.Message.PlainText())
	}
	if run != nil {
		t.Error("an empty /suggestdeckbox must not schedule work")
	}
}

func TestSuggestWording(t *testing.T) {
	dbx := &fakeDomain{suggestResult: deckbox.SuggestResult{
		ProcessedCount: 2, SkippedCount: 1, Errors: []string{"masha: not found"},
	}}
	ack, run := newTestCommands(dbx).Suggest(req("petya\nmasha\nvasya"))

	if got := ack.Message.PlainText(); got != "Starting processing of 3 logins..." {
		t.Errorf("ack = %q", got)
	}
	want := "Processed: 2\nSkipped: 1\n\nErrors:\n• masha: not found\n"
	if got := run(context.Background()).Message.PlainText(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// --- Start ---

func TestStartIsTheWelcome(t *testing.T) {
	replies := newTestCommands(&fakeDomain{}).Start(context.Background(), req(""))

	if len(replies) != 1 || !strings.HasPrefix(replies[0].Message.PlainText(), "Hello!\n\n") {
		t.Fatalf("expected the welcome, got %+v", replies)
	}
}

func TestStartWordingFollowsPlatform(t *testing.T) {
	c := newTestCommands(&fakeDomain{})
	for _, l := range []string{"en", "ru"} {
		r := req("")
		r.Lang = l
		r.Platform = deckbox.PlatformDiscord
		d := c.Start(context.Background(), r)[0].Message.PlainText()
		r.Platform = deckbox.PlatformTelegram
		tg := c.Start(context.Background(), r)[0].Message.PlainText()
		if !strings.Contains(d, "/search") || strings.Contains(d, "Just send") {
			t.Errorf("%s discord welcome wrong: %q", l, d)
		}
		if strings.Contains(tg, "/search") {
			t.Errorf("%s telegram welcome mentions /search: %q", l, tg)
		}
		if l == "en" && !strings.Contains(tg, "Just send") {
			t.Errorf("telegram welcome lost its wording: %q", tg)
		}
	}
}

func TestPrivateReplies(t *testing.T) {
	ctx := context.Background()
	found := deckbox.SearchResult{
		SearchQueries: []string{"Shock"},
		Aggregates: []deckbox.UserSearchAggregate{{ListId: 1, DeckboxLogin: "masha",
			FoundCards: map[string]int16{"Shock": 2}, UniqueCount: 1, TotalQuantity: 2}},
	}
	missing := deckbox.SearchResult{SearchQueries: []string{"Ragavan"}, NotFound: []string{"Ragavan"}}
	failing := &fakeDomain{registerErr: errors.New("boom")}

	cases := []struct {
		name    string
		replies func() []Reply
		private bool
	}{
		{"start", func() []Reply { return newTestCommands(&fakeDomain{}).Start(ctx, req("")) }, true},
		{"register ok", func() []Reply { return newTestCommands(&fakeDomain{}).Register(ctx, req("masha")) }, true},
		{"register no arg", func() []Reply { return newTestCommands(&fakeDomain{}).Register(ctx, req("")) }, true},
		{"register error", func() []Reply { return newTestCommands(failing).Register(ctx, req("masha")) }, true},
		{"sell no arg", func() []Reply { return newTestCommands(&fakeDomain{}).Sell(ctx, req("")) }, true},
		{"suggest no arg", func() []Reply { a, _ := newTestCommands(&fakeDomain{}).Suggest(req("")); return []Reply{a} }, true},
		{"suggest ack", func() []Reply { a, _ := newTestCommands(&fakeDomain{}).Suggest(req("a\nb")); return []Reply{a} }, true},
		{"suggest summary", func() []Reply {
			_, f := newTestCommands(&fakeDomain{}).Suggest(req("a"))
			return []Reply{f(ctx)}
		}, false},
		{"search", func() []Reply { return newTestCommands(&fakeDomain{searchResult: found}).Search(ctx, req("Shock")) }, false},
		{"sell", func() []Reply { return newTestCommands(&fakeDomain{searchResult: found}).Sell(ctx, req("Shock")) }, false},
		{"not found", func() []Reply { return newTestCommands(&fakeDomain{searchResult: missing}).Search(ctx, req("Ragavan")) }, false},
	}
	for _, tc := range cases {
		rs := tc.replies()
		if len(rs) == 0 {
			t.Errorf("%s: no replies", tc.name)
		}
		for i, r := range rs {
			if r.Private != tc.private {
				t.Errorf("%s reply %d: Private=%v, want %v", tc.name, i, r.Private, tc.private)
			}
		}
	}
}
