package admin

import (
	"FriendlyCardFinder/internal/deckbox"
	"strings"
	"testing"
	"time"
)

// The view builders are pure — domain values in, view rows out — so the numbers
// the Admin Panel shows are testable without a browser or an HTTP request. What
// they compute is arithmetic an operator acts on (a Purge is irreversible), so
// it is worth pinning.

func ptrInt64(n int64) *int64      { return &n }
func ptrStr(s string) *string      { return &s }
func ago64(d time.Duration) *int64 { return ptrInt64(time.Now().Add(-d).Unix()) }

func TestBuildManageViewCounts(t *testing.T) {
	// Three collections, two of them claimed by a Bot User, plus one Orphan
	// Registration — a login someone typed that never produced a collection.
	overview := deckbox.Overview{
		BotUsers:      3,
		StaleUsers:    1,
		CardRows:      200264,
		DistinctCards: 12000,
		TotalQuantity: 240000,
		Orphans: []deckbox.OrphanRegistration{
			{TelegramID: 9, DeckboxLogin: "typo_login"},
		},
	}
	users := []deckbox.AdminUser{
		{DeckboxLogin: "petya", TelegramID: ptrInt64(1), TelegramUsername: ptrStr("petya_tg"), MissingLists: 0, UpdatedAt: ago64(time.Hour)},
		{DeckboxLogin: "masha", TelegramID: ptrInt64(2), MissingLists: 1, UpdatedAt: ago64(30 * time.Hour)},
		{DeckboxLogin: "vasya", MissingLists: 2, UpdatedAt: nil},
	}

	v := buildManageView(overview, users, "purged someone")

	if v.Message != "purged someone" {
		t.Errorf("Message = %q, want the action message passed through", v.Message)
	}
	if len(v.Users) != len(users) {
		t.Fatalf("expected one row per collection, got %d", len(v.Users))
	}

	tiles := map[string]tile{}
	for _, tl := range v.Tiles {
		tiles[tl.Label] = tl
	}

	if got := tiles["Deckbox Users"].Value; got != "3" {
		t.Errorf("Deckbox Users = %q, want 3", got)
	}
	// Missing lists is a sum over the same slice the table renders, so the strip
	// can never disagree with the rows below it.
	if got := tiles["Missing lists"].Value; got != "3" {
		t.Errorf("Missing lists = %q, want 0+1+2 = 3", got)
	}
	// 3 Bot Users, 1 of them an Orphan, so 2 match a collection — leaving 1 of
	// the 3 collections with nobody claiming it.
	if note := tiles["Bot Users"].Note; !strings.HasPrefix(note, "1 collection") {
		t.Errorf("Bot Users note = %q, want it to report 1 unclaimed collection", note)
	}
	if got := tiles["Card rows"].Value; got != "200,264" {
		t.Errorf("Card rows = %q, want thousands grouped", got)
	}

	// A row with no timestamp has never refreshed, which the table flags.
	if !v.Users[2].Stale || v.Users[2].Age != "never" {
		t.Errorf("a collection that never refreshed should read as stale/never, got %+v", v.Users[2])
	}
	if v.Users[0].Telegram != "@petya_tg" {
		t.Errorf("Telegram = %q, want the @username", v.Users[0].Telegram)
	}
	if v.Users[1].Telegram != "id 2" {
		t.Errorf("a registration without a username should fall back to the id, got %q", v.Users[1].Telegram)
	}
	if v.Users[2].Telegram != "—" {
		t.Errorf("an unregistered collection should show a dash, got %q", v.Users[2].Telegram)
	}
}

func TestCountTileColoursOnlyNonZero(t *testing.T) {
	// Zero stale users is not a warning; one is. Status drives the colour, so a
	// permanently amber "0" would train the operator to ignore it.
	if got := countTile("Stale", "note", 0, "warn").Status; got != "" {
		t.Errorf("zero must carry no status, got %q", got)
	}
	if got := countTile("Stale", "note", 1, "warn").Status; got != "warn" {
		t.Errorf("non-zero must carry the status, got %q", got)
	}
}

func TestBuildConfirmView(t *testing.T) {
	users := []deckbox.AdminUser{
		{DeckboxLogin: "petya", TelegramID: ptrInt64(1), InventoryCount: 1200, TradelistCount: 40, WishlistCount: 3},
	}

	t.Run("names what the purge will remove", func(t *testing.T) {
		v, ok := buildConfirmView(users, "petya")
		if !ok {
			t.Fatal("expected the known login to be found")
		}
		if v.Inventory != "1,200" || v.Tradelist != "40" || v.Wishlist != "3" {
			t.Errorf("counts = %q/%q/%q, want the collection's sizes", v.Inventory, v.Tradelist, v.Wishlist)
		}
		if !v.HasRegistration {
			t.Error("a claimed login must report that a registration goes too")
		}
	})

	t.Run("refuses a login with no collection", func(t *testing.T) {
		// The confirmation page must never name a collection that does not
		// exist — the operator is about to approve an irreversible delete.
		if _, ok := buildConfirmView(users, "nobody"); ok {
			t.Fatal("an unknown login must not produce a confirmation page")
		}
	})
}

func TestBuildInsightsViewBars(t *testing.T) {
	in := deckbox.Insights{
		TopMisses: []deckbox.TermDemand{
			{Term: "ragavan", Scope: deckbox.ScopeTradelist, Hits: 0, Misses: 40, LastSeen: time.Now().Unix()},
			{Term: "bolt", Scope: deckbox.ScopeTradelist, Hits: 12, Misses: 1, LastSeen: time.Now().Unix()},
		},
		MostWanted:      []deckbox.CardDemand{{CardName: "Ragavan", Wishers: 4}},
		TradeMatchTotal: 1500,
		Supply:          []deckbox.SupplyBucket{{Owners: 1, Cards: 900}, {Owners: 3, Cards: 100}},
	}

	v := buildInsightsView(in, time.Now())

	tiles := map[string]string{}
	for _, tl := range v.Tiles {
		tiles[tl.Label] = tl.Value
	}
	if tiles["Searched & missed"] != "41" || tiles["Searched & found"] != "12" {
		t.Errorf("hit/miss totals = %q/%q, want 41/12", tiles["Searched & missed"], tiles["Searched & found"])
	}
	if tiles["Single-source cards"] != "900" {
		t.Errorf("single-source = %q, want the Owners==1 bucket", tiles["Single-source cards"])
	}
	if tiles["Trade Matches"] != "1,500" {
		t.Errorf("trade matches = %q, want thousands grouped", tiles["Trade Matches"])
	}

	// Bars are scaled against the widest value in view, not against a total.
	if v.Demand[0].Width != 100 {
		t.Errorf("the largest miss count should fill the bar, got %d", v.Demand[0].Width)
	}
	if v.Demand[1].Width != 2 {
		t.Errorf("1 of 40 should be 2%%, got %d", v.Demand[1].Width)
	}
	if v.Demand[0].Scope != deckbox.ScopeTradelist {
		t.Errorf("Scope should reach the row, got %q", v.Demand[0].Scope)
	}
}

func TestPercentKeepsSmallValuesVisible(t *testing.T) {
	// A non-zero value flooring to 0% would draw an empty bar and lie about
	// being empty; a genuine zero must stay empty.
	tests := []struct{ n, max, want int }{
		{0, 100, 0},
		{1, 1000, 1}, // floors to 0, lifted to 1
		{50, 100, 50},
		{100, 100, 100},
		{5, 0, 0}, // no maximum yet: nothing to scale against
	}
	for _, tt := range tests {
		if got := percent(tt.n, tt.max); got != tt.want {
			t.Errorf("percent(%d, %d) = %d, want %d", tt.n, tt.max, got, tt.want)
		}
	}
}

func TestBuildSpark(t *testing.T) {
	t.Run("no data draws nothing", func(t *testing.T) {
		s := buildSpark(nil)
		if !s.Empty || s.Points != "" {
			t.Errorf("an empty series must report Empty with no points, got %+v", s)
		}
	})

	t.Run("a single day sits at the right edge", func(t *testing.T) {
		// There is no line to draw, so the point goes where the next one would
		// continue from rather than floating in the middle.
		s := buildSpark([]deckbox.DayCount{{Day: "2026-09-07", Searches: 5, Misses: 1}})
		if len(s.Marks) != 1 {
			t.Fatalf("expected one mark, got %d", len(s.Marks))
		}
		if s.Marks[0].X != sparkWidth-sparkPadX {
			t.Errorf("X = %d, want the right edge %d", s.Marks[0].X, sparkWidth-sparkPadX)
		}
		if s.Latest != "5" || s.Max != "5" {
			t.Errorf("Latest/Max = %q/%q, want 5/5", s.Latest, s.Max)
		}
	})

	t.Run("the series is inset and scaled to its own maximum", func(t *testing.T) {
		days := []deckbox.DayCount{
			{Day: "2026-09-05", Searches: 0},
			{Day: "2026-09-06", Searches: 10},
			{Day: "2026-09-07", Searches: 5},
		}
		s := buildSpark(days)

		if len(s.Marks) != 3 {
			t.Fatalf("expected one mark per day, got %d", len(s.Marks))
		}
		if s.FirstDay != "2026-09-05" || s.LastDay != "2026-09-07" {
			t.Errorf("axis labels = %q..%q, want the first and last day", s.FirstDay, s.LastDay)
		}
		if s.Latest != "5" || s.Max != "10" {
			t.Errorf("Latest/Max = %q/%q, want 5/10", s.Latest, s.Max)
		}

		// The stroke and the end marker must never be clipped by the viewBox.
		for i, m := range s.Marks {
			if m.X < sparkPadX || m.X > sparkWidth-sparkPadX {
				t.Errorf("mark %d X = %d, outside the inset plot", i, m.X)
			}
			if m.Y < sparkPadY || m.Y > sparkHeight-sparkPadY {
				t.Errorf("mark %d Y = %d, outside the inset plot", i, m.Y)
			}
		}
		// Higher is drawn further up the SVG, i.e. a smaller Y.
		if s.Marks[1].Y >= s.Marks[2].Y {
			t.Error("the busier day should plot above the quieter one")
		}
		if !strings.Contains(s.Marks[0].Title, "2026-09-05") {
			t.Errorf("each point needs a readable title, got %q", s.Marks[0].Title)
		}
	})
}

func TestAgo(t *testing.T) {
	tests := []struct {
		name string
		ts   *int64
		want string
	}{
		{"never happened", nil, "never"},
		{"zero timestamp is also never", ptrInt64(0), "never"},
		{"seconds", ago64(10 * time.Second), "just now"},
		{"minutes", ago64(5 * time.Minute), "5m ago"},
		{"hours", ago64(3 * time.Hour), "3h ago"},
		{"days", ago64(50 * time.Hour), "2d ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ago(tt.ts); got != tt.want {
				t.Errorf("ago() = %q, want %q", got, tt.want)
			}
		})
	}
}
