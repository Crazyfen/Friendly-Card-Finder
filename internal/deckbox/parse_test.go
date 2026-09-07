package deckbox

import (
	"io"
	"log/slog"
	"reflect"
	"testing"
)

// These tests cross the seam parseExport and parseProfile sit on: they take
// bytes, so they need neither an account nor a network. The fixtures are shaped
// like the real pages rather than captured from them — TestUserScrapper and
// TestCardListScrapper (network, credential-gated) are still what proves the
// shape is right.

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// exportPage wraps export lines the way Deckbox serves them.
func exportPage(inner string) []byte {
	return []byte("<html><head><title>Export</title></head><body class='export'>" + inner + "</body></html>")
}

func TestParseExport(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]int16
	}{
		{
			name: "one line per card",
			body: "2 Lightning Bolt<br/>1 Shock<br/>4 Counterspell",
			want: map[string]int16{"Lightning Bolt": 2, "Shock": 1, "Counterspell": 4},
		},
		{
			name: "printings of one card are summed",
			// The export carries no variation data, so the same name on several
			// lines is the same card in different printings — see docs/adr/0008.
			// Their total is the only Quantity that exists.
			body: "2 Shock<br/>3 Shock<br/>1 Shock",
			want: map[string]int16{"Shock": 6},
		},
		{
			name: "html entities are unescaped",
			body: "1 &quot;Brims&quot; Barone, Midway Mobster<br/>2 Ratchet, Field Medic",
			want: map[string]int16{`"Brims" Barone, Midway Mobster`: 1, "Ratchet, Field Medic": 2},
		},
		{
			name: "surrounding whitespace and blank entries are ignored",
			body: "\n  2 Lightning Bolt  \n<br/>\n<br/>  1 Shock\n",
			want: map[string]int16{"Lightning Bolt": 2, "Shock": 1},
		},
		{
			name: "names keep their internal punctuation and spacing",
			body: "1 Vitu-Ghazi, the City-Tree<br/>1 Sméagol, Helpful Guide<br/>1 Fire // Ice",
			want: map[string]int16{"Vitu-Ghazi, the City-Tree": 1, "Sméagol, Helpful Guide": 1, "Fire // Ice": 1},
		},
		{
			name: "malformed entries are skipped, the rest survive",
			body: "2 Lightning Bolt<br/>notanumber Shock<br/>Counterspell<br/>3 Ragavan",
			want: map[string]int16{"Lightning Bolt": 2, "Ragavan": 3},
		},
		{
			name: "an empty list is an empty map, not a failure",
			body: "",
			want: map[string]int16{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, authFailed := parseExport(42, exportPage(tt.body), quietLog())
			if authFailed {
				t.Fatal("a valid export must not read as an auth failure")
			}
			if got.ListId != 42 {
				t.Errorf("ListId = %d, want 42", got.ListId)
			}
			if !reflect.DeepEqual(got.Cards, tt.want) {
				t.Errorf("Cards = %v, want %v", got.Cards, tt.want)
			}
		})
	}
}

func TestParseExportDetectsTheLoginPage(t *testing.T) {
	// A rejected session cookie can arrive as a 200 carrying the login form. If
	// this went unnoticed the Card List would parse as empty and overwrite a real
	// collection with nothing.
	pages := map[string]string{
		"single-quoted token": `<form><input name='authenticity_token' value='abc'/></form>`,
		"double-quoted token": `<form><input name="authenticity_token" value="abc"/></form>`,
	}

	for name, inner := range pages {
		t.Run(name, func(t *testing.T) {
			got, authFailed := parseExport(42, exportPage(inner), quietLog())
			if !authFailed {
				t.Fatalf("the login page must report an auth failure, got %+v", got)
			}
			if len(got.Cards) != 0 || got.BodyHash != 0 {
				t.Errorf("an auth failure must produce no Card List, got %+v", got)
			}
		})
	}
}

func TestParseExportBodyHash(t *testing.T) {
	// The hash is what makes an unchanged Card List free to confirm (ADR-0005),
	// so it has to be stable for identical exports and different for changed
	// ones — including a change that leaves the card totals alone.
	same1, _ := parseExport(1, exportPage("2 Shock<br/>1 Bolt"), quietLog())
	same2, _ := parseExport(1, exportPage("2 Shock<br/>1 Bolt"), quietLog())
	changed, _ := parseExport(1, exportPage("3 Shock<br/>1 Bolt"), quietLog())
	reordered, _ := parseExport(1, exportPage("1 Bolt<br/>2 Shock"), quietLog())

	if same1.BodyHash == 0 {
		t.Fatal("a parsed export must carry a hash")
	}
	if same1.BodyHash != same2.BodyHash {
		t.Error("identical exports must hash the same, or every refresh rewrites the list")
	}
	if same1.BodyHash == changed.BodyHash {
		t.Error("a changed export must hash differently, or the change is never saved")
	}
	if same1.BodyHash == reordered.BodyHash {
		t.Error("the hash is of the body, not of the parsed cards — reordering must differ")
	}
}

func TestParseProfile(t *testing.T) {
	const full = `<html><body>
	<div id="section_mtg">
		<div class="submenu_entry t_inv"><a href="/sets/111">Inventory</a></div>
		<div class="submenu_entry t_trade"><a href="/sets/222">Tradelist</a></div>
		<div class="submenu_entry t_wish"><a href="/sets/333">Wishlist</a></div>
	</div></body></html>`

	// A Deckbox User who never made a wishlist: the entry is simply absent.
	const noWishlist = `<html><body>
	<div id="section_mtg">
		<div class="submenu_entry t_inv"><a href="/sets/111">Inventory</a></div>
		<div class="submenu_entry t_trade"><a href="/sets/222">Tradelist</a></div>
	</div></body></html>`

	// Another game's section must not be read as Magic lists.
	const otherGame = `<html><body>
	<div id="section_ygo">
		<div class="submenu_entry t_inv"><a href="/sets/999">Inventory</a></div>
	</div></body></html>`

	id := func(n int64) *int64 { return &n }

	tests := []struct {
		name             string
		page             string
		inv, trade, wish *int64
	}{
		{"all three lists", full, id(111), id(222), id(333)},
		{"missing wishlist stays nil", noWishlist, id(111), id(222), nil},
		{"a non-Magic section is ignored", otherGame, nil, nil, nil},
		{"an empty page yields no lists", `<html><body></body></html>`, nil, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProfile("petya", []byte(tt.page))
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if got.DeckboxLogin != "petya" {
				t.Errorf("DeckboxLogin = %q, want petya", got.DeckboxLogin)
			}
			assertListID(t, "inventory", got.InventoryID, tt.inv)
			assertListID(t, "tradelist", got.TradelistID, tt.trade)
			assertListID(t, "wishlist", got.WishlistID, tt.wish)
		})
	}
}

func assertListID(t *testing.T, name string, got, want *int64) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil:
		t.Errorf("%s = nil, want %d", name, *want)
	case want == nil:
		t.Errorf("%s = %d, want nil", name, *got)
	case *got != *want:
		t.Errorf("%s = %d, want %d", name, *got, *want)
	}
}
