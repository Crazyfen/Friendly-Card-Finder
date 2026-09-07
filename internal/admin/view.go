package admin

import (
	"FriendlyCardFinder/internal/deckbox"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

var (
	manageTemplate   = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/manage.html"))
	insightsTemplate = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/insights.html"))
	confirmTemplate  = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/confirm.html"))
)

// tile is one number in the stat strip. A count with a label reads faster than
// any chart of it, so the strip is deliberately not plotted.
type tile struct {
	Label  string
	Value  string
	Note   string
	Status string // "", "warn", "bad" — always paired with the note, never colour alone
}

type userRow struct {
	Login     string
	Telegram  string
	Inventory string
	Tradelist string
	Wishlist  string
	Age       string
	Stale     bool
	Missing   int
	LastError string
}

type manageView struct {
	Page    string
	Message string
	Tiles   []tile
	Users   []userRow
	Orphans []deckbox.OrphanRegistration
}

type demandRow struct {
	Term   string
	Scope  deckbox.Scope
	Hits   int
	Misses int
	Last   string
	Width  int // % of the widest miss count in view
}

type wantRow struct {
	CardName string
	Wishers  int
	Width    int
}

type supplyRow struct {
	Owners int
	Cards  string
	Width  int
}

// spark is a single-series line: no legend (the heading names it), one direct
// label on the last point, and a native <title> per point instead of a
// scripted tooltip.
type spark struct {
	Empty    bool
	Width    int
	Height   int
	Points   string
	Marks    []sparkMark
	Max      string
	Latest   string
	FirstDay string
	LastDay  string
}

type sparkMark struct {
	X, Y  int
	Title string
}

type insightsView struct {
	Page       string
	ComputedAt string
	Tiles      []tile
	Demand     []demandRow
	MostWanted []wantRow
	Trades     []deckbox.TradeMatch
	TradeTotal string
	TradeShown int
	Supply     []supplyRow
	Spark      spark
}

type confirmView struct {
	Page            string
	Login           string
	Inventory       string
	Tradelist       string
	Wishlist        string
	Telegram        string
	HasRegistration bool
}

// buildConfirmView finds what a Purge of login would remove. It reports false
// for a login with no collection, so the confirmation page can never name one
// that does not exist.
func buildConfirmView(users []deckbox.AdminUser, login string) (confirmView, bool) {
	for _, u := range users {
		if u.DeckboxLogin != login {
			continue
		}
		return confirmView{
			Page:            "manage",
			Login:           u.DeckboxLogin,
			Inventory:       humanCount(int64(u.InventoryCount)),
			Tradelist:       humanCount(int64(u.TradelistCount)),
			Wishlist:        humanCount(int64(u.WishlistCount)),
			Telegram:        telegramLabel(u),
			HasRegistration: u.TelegramID != nil,
		}, true
	}
	return confirmView{}, false
}

func buildManageView(o deckbox.Overview, users []deckbox.AdminUser, msg string) manageView {
	v := manageView{Page: "manage", Message: msg}

	// Both counts are sums over the same slice the table renders, so the strip
	// can never disagree with the rows below it.
	missingLists := 0
	for _, u := range users {
		missingLists += u.MissingLists
	}

	// Orphans are registrations that match no collection, so they do not count
	// towards the collections that have one.
	matched := o.BotUsers - len(o.Orphans)
	registeredNote := fmt.Sprintf("%d collections have no registration", len(users)-matched)

	v.Tiles = []tile{
		countTile("Deckbox Users", "collections tracked", len(users), ""),
		countTile("Bot Users", registeredNote, o.BotUsers, ""),
		{Label: "Card rows", Value: humanCount(o.CardRows), Note: humanCount(o.DistinctCards) + " distinct names"},
		{Label: "Total quantity", Value: humanCount(o.TotalQuantity), Note: "cards across all lists"},
		countTile("Stale", "past the refresh window", o.StaleUsers, "warn"),
		countTile("Missing lists", "absent or empty, of 3 per user", missingLists, "warn"),
		countTile("Orphan registrations", "claimed logins with no collection", len(o.Orphans), "bad"),
	}

	for _, u := range users {
		v.Users = append(v.Users, userRow{
			Login:     u.DeckboxLogin,
			Telegram:  telegramLabel(u),
			Inventory: humanCount(int64(u.InventoryCount)),
			Tradelist: humanCount(int64(u.TradelistCount)),
			Wishlist:  humanCount(int64(u.WishlistCount)),
			Age:       ago(u.UpdatedAt),
			Stale:     u.UpdatedAt == nil,
			Missing:   u.MissingLists,
			LastError: u.LastError,
		})
	}

	v.Orphans = o.Orphans
	return v
}

// countTile renders a count whose status only applies when it is non-zero:
// zero stale users is not a warning, one is. An empty status is never coloured.
func countTile(label, note string, n int, status string) tile {
	t := tile{Label: label, Value: humanCount(int64(n)), Note: note}
	if n > 0 {
		t.Status = status
	}
	return t
}

func telegramLabel(u deckbox.AdminUser) string {
	switch {
	case u.TelegramUsername != nil && *u.TelegramUsername != "":
		return "@" + *u.TelegramUsername
	case u.TelegramID != nil:
		return "id " + strconv.FormatInt(*u.TelegramID, 10)
	default:
		return "—"
	}
}

func buildInsightsView(in deckbox.Insights, computedAt time.Time) insightsView {
	v := insightsView{
		Page:       "insights",
		ComputedAt: computedAt.Local().Format("15:04:05"),
		TradeTotal: humanCount(int64(in.TradeMatchTotal)),
		TradeShown: len(in.TradeMatches),
		Trades:     in.TradeMatches,
	}

	var totalMisses, totalHits int
	for _, d := range in.TopMisses {
		totalMisses += d.Misses
		totalHits += d.Hits
	}
	singleOwner := 0
	for _, b := range in.Supply {
		if b.Owners == 1 {
			singleOwner = b.Cards
		}
	}

	v.Tiles = []tile{
		{Label: "Trade Matches", Value: humanCount(int64(in.TradeMatchTotal)), Note: "wishlist cards someone else trades"},
		{Label: "Searched & missed", Value: humanCount(int64(totalMisses)), Note: "in the top terms below"},
		{Label: "Searched & found", Value: humanCount(int64(totalHits)), Note: "in the top terms below"},
		{Label: "Single-source cards", Value: humanCount(int64(singleOwner)), Note: "only one tradelist carries them"},
	}

	maxMiss := 1
	for _, d := range in.TopMisses {
		if d.Misses > maxMiss {
			maxMiss = d.Misses
		}
	}
	for _, d := range in.TopMisses {
		v.Demand = append(v.Demand, demandRow{
			Term:   d.Term,
			Scope:  d.Scope,
			Hits:   d.Hits,
			Misses: d.Misses,
			Last:   ago(&d.LastSeen),
			Width:  percent(d.Misses, maxMiss),
		})
	}

	maxWishers := 1
	for _, c := range in.MostWanted {
		if c.Wishers > maxWishers {
			maxWishers = c.Wishers
		}
	}
	for _, c := range in.MostWanted {
		v.MostWanted = append(v.MostWanted, wantRow{
			CardName: c.CardName,
			Wishers:  c.Wishers,
			Width:    percent(c.Wishers, maxWishers),
		})
	}

	maxCards := 1
	for _, b := range in.Supply {
		if b.Cards > maxCards {
			maxCards = b.Cards
		}
	}
	for _, b := range in.Supply {
		v.Supply = append(v.Supply, supplyRow{
			Owners: b.Owners,
			Cards:  humanCount(int64(b.Cards)),
			Width:  percent(b.Cards, maxCards),
		})
	}

	v.Spark = buildSpark(in.Daily)
	return v
}

// Sparkline geometry. The plot is inset so the 2px stroke and the end marker
// are never clipped by the viewBox.
const (
	sparkWidth   = 720
	sparkHeight  = 90
	sparkPadX    = 6
	sparkPadY    = 10
	sparkMarkMin = 8
)

// buildSpark lays out the daily search series. One series, so there is no
// legend and no second axis — the heading names the measure, and only the last
// point is labelled.
func buildSpark(days []deckbox.DayCount) spark {
	if len(days) == 0 {
		return spark{Empty: true, Width: sparkWidth, Height: sparkHeight}
	}

	maxSearches := 1
	for _, d := range days {
		if d.Searches > maxSearches {
			maxSearches = d.Searches
		}
	}

	plotW := sparkWidth - 2*sparkPadX
	plotH := sparkHeight - 2*sparkPadY

	sp := spark{
		Width:    sparkWidth,
		Height:   sparkHeight,
		Max:      humanCount(int64(maxSearches)),
		Latest:   humanCount(int64(days[len(days)-1].Searches)),
		FirstDay: days[0].Day,
		LastDay:  days[len(days)-1].Day,
	}

	// A single day has no line to draw, so it is placed at the right edge where
	// the next one would continue from.
	step := 0.0
	if len(days) > 1 {
		step = float64(plotW) / float64(len(days)-1)
	}

	var points strings.Builder
	for i, d := range days {
		x := sparkPadX + int(step*float64(i))
		if len(days) == 1 {
			x = sparkWidth - sparkPadX
		}
		y := sparkPadY + plotH - int(float64(plotH)*float64(d.Searches)/float64(maxSearches))

		if i > 0 {
			points.WriteByte(' ')
		}
		fmt.Fprintf(&points, "%d,%d", x, y)

		sp.Marks = append(sp.Marks, sparkMark{
			X:     x,
			Y:     y,
			Title: fmt.Sprintf("%s — %d searches, %d missed", d.Day, d.Searches, d.Misses),
		})
	}
	sp.Points = points.String()

	return sp
}

func percent(n, max int) int {
	if max <= 0 {
		return 0
	}
	p := n * 100 / max
	if p < 1 && n > 0 {
		// A non-zero value must stay visible, or the bar lies about being empty.
		p = 1
	}
	return p
}

// printer groups thousands (200,264) so six-figure card counts stay readable.
// golang.org/x/text is already a dependency for the search normalizer.
var printer = message.NewPrinter(language.English)

func humanCount(n int64) string {
	return printer.Sprintf("%d", n)
}

// ago renders a unix timestamp as an age. Nil means it never happened.
func ago(ts *int64) string {
	if ts == nil || *ts == 0 {
		return "never"
	}

	d := time.Since(time.Unix(*ts, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
