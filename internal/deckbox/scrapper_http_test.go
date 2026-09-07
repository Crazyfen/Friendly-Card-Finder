package deckbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// These drive the Scraper's transport half against a stand-in Deckbox. Only the
// host is swapped: the retries, the redirect-to-login detection, the re-login
// handshake and the export fetch are the same code production runs.

// fakeDeckbox serves the three pages the Scraper knows how to ask for, and
// counts what it was asked.
type fakeDeckbox struct {
	*httptest.Server

	logins  atomic.Int64
	exports atomic.Int64

	// acceptCookie is the only session cookie the export honours; empty accepts
	// any. Naming one that no login issues simulates a permanently rejected one.
	acceptCookie string

	// failExports makes the first N export attempts answer 500, to exercise the
	// retry.
	failExports int64

	// loginPostStatus is what the credential POST answers with. 302 is success;
	// 200 means Deckbox re-rendered the form, i.e. bad credentials.
	loginPostStatus int
}

const fakeExport = "2 Lightning Bolt<br/>1 Shock"

func newFakeDeckbox(t *testing.T) *fakeDeckbox {
	t.Helper()
	f := &fakeDeckbox{loginPostStatus: http.StatusFound}

	mux := http.NewServeMux()

	mux.HandleFunc("/accounts/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The form the CSRF token is scraped from.
			fmt.Fprint(w, `<html><body><form><input name="authenticity_token" value="tok-123"/></form></body></html>`)
			return
		}
		n := f.logins.Add(1)
		if f.loginPostStatus == http.StatusFound {
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: fmt.Sprintf("session-%d", n), Path: "/"})
			w.Header().Set("Location", "/")
		}
		w.WriteHeader(f.loginPostStatus)
	})

	mux.HandleFunc("/sets/", func(w http.ResponseWriter, r *http.Request) {
		if n := f.exports.Add(1); n <= f.failExports {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var cookie string
		if c, err := r.Cookie(sessionCookieName); err == nil {
			cookie = c.Value
		}
		if f.acceptCookie != "" && cookie != f.acceptCookie {
			// A rejected cookie is a redirect to the login page, not a 401.
			http.Redirect(w, r, "/accounts/login", http.StatusFound)
			return
		}
		fmt.Fprintf(w, "<html><body>%s</body></html>", fakeExport)
	})

	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><div id="section_mtg">
			<div class="submenu_entry t_inv"><a href="/sets/111">Inventory</a></div>
			<div class="submenu_entry t_trade"><a href="/sets/222">Tradelist</a></div>
			<div class="submenu_entry t_wish"><a href="/sets/333">Wishlist</a></div>
		</div></body></html>`)
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// scraperAgainst builds a Scraper pointed at the stand-in.
func scraperAgainst(t *testing.T, f *fakeDeckbox) *Scraper {
	t.Helper()
	s := NewScraper(quietLog(), ScraperAuth{
		Login:      "user",
		Password:   "pass",
		CookiePath: t.TempDir() + "/deckbox_session",
	})
	s.baseURL = f.URL
	return s
}

func TestFetchCardListLogsInAndParses(t *testing.T) {
	f := newFakeDeckbox(t)
	s := scraperAgainst(t, f)

	list, err := s.FetchCardList(context.Background(), 222)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	if list.ListId != 222 {
		t.Errorf("ListId = %d, want 222", list.ListId)
	}
	if list.Cards["Lightning Bolt"] != 2 || list.Cards["Shock"] != 1 {
		t.Errorf("Cards = %v, want the served export", list.Cards)
	}
	if list.BodyHash == 0 {
		t.Error("a fetched Card List must carry a body hash")
	}
	if got := f.logins.Load(); got != 1 {
		t.Errorf("logins = %d, want 1 — a cold start logs in once", got)
	}
}

func TestFetchCardListRecoversFromARejectedCookie(t *testing.T) {
	// The rule the bot runs unattended on: an expired session shows up as the
	// login page inside a 200, and must self-heal without anyone noticing.
	f := newFakeDeckbox(t)
	f.acceptCookie = "session-2" // only the cookie the second login issues
	s := scraperAgainst(t, f)

	list, err := s.FetchCardList(context.Background(), 222)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(list.Cards) != 2 {
		t.Errorf("Cards = %v, want the export after re-login", list.Cards)
	}
	if got := f.logins.Load(); got != 2 {
		t.Errorf("logins = %d, want exactly one re-login", got)
	}
	if got := f.exports.Load(); got != 2 {
		t.Errorf("export fetches = %d, want the rejected one and the retry", got)
	}
}

func TestFetchCardListGivesUpAfterOneRelogin(t *testing.T) {
	// Still rejected after re-logging in must be an error. Returning the login
	// page as an empty Card List would overwrite a real collection with nothing.
	f := newFakeDeckbox(t)
	f.acceptCookie = "never-issued" // no login will ever produce this
	s := scraperAgainst(t, f)

	_, err := s.FetchCardList(context.Background(), 222)
	if err == nil {
		t.Fatal("expected an error when the cookie is rejected twice")
	}
	if !strings.Contains(err.Error(), "still unauthenticated") {
		t.Errorf("error = %v, want it to name the cause", err)
	}
	if got := f.logins.Load(); got != 2 {
		t.Errorf("logins = %d, want it to stop after one retry", got)
	}
}

func TestFetchRetriesTransientFailures(t *testing.T) {
	f := newFakeDeckbox(t)
	f.failExports = 2 // two failures then success, within the three fetchPage allows
	s := scraperAgainst(t, f)

	list, err := s.FetchCardList(context.Background(), 222)
	if err != nil {
		t.Fatalf("fetch should survive two transient failures: %v", err)
	}
	if len(list.Cards) != 2 {
		t.Errorf("Cards = %v, want the export from the third attempt", list.Cards)
	}
	if got := f.exports.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestFetchGivesUpAfterThreeAttempts(t *testing.T) {
	f := newFakeDeckbox(t)
	f.failExports = 3 // every attempt fetchPage will make
	s := scraperAgainst(t, f)

	if _, err := s.FetchCardList(context.Background(), 222); err == nil {
		t.Fatal("expected an error when every attempt fails")
	}
	if got := f.exports.Load(); got != 3 {
		t.Errorf("attempts = %d, want the retry to be bounded at 3", got)
	}
}

func TestLoginFailureIsNotSuccess(t *testing.T) {
	// Deckbox answers bad credentials by re-rendering the form with a 200. A
	// scraper that read that as success would fetch every list unauthenticated.
	f := newFakeDeckbox(t)
	f.loginPostStatus = http.StatusOK
	s := scraperAgainst(t, f)

	_, err := s.FetchCardList(context.Background(), 222)
	if err == nil {
		t.Fatal("expected an error when the login POST is refused")
	}
	if !strings.Contains(err.Error(), "login failed") {
		t.Errorf("error = %v, want it to name the login", err)
	}
}

func TestNoCredentialsIsAnError(t *testing.T) {
	f := newFakeDeckbox(t)
	s := NewScraper(quietLog(), ScraperAuth{CookiePath: t.TempDir() + "/deckbox_session"})
	s.baseURL = f.URL

	if _, err := s.FetchCardList(context.Background(), 222); err == nil {
		t.Fatal("expected an error when there is nothing to log in with")
	}
	if got := f.logins.Load(); got != 0 {
		t.Errorf("logins = %d, want none attempted", got)
	}
}

func TestFetchDeckboxUserProfileNeedsNoCookie(t *testing.T) {
	// Profiles are public, so the profile fetch must not drag a login in with it.
	f := newFakeDeckbox(t)
	s := scraperAgainst(t, f)

	user, err := s.FetchDeckboxUserProfile(context.Background(), "petya")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if user.DeckboxLogin != "petya" {
		t.Errorf("DeckboxLogin = %q, want petya", user.DeckboxLogin)
	}
	if user.InventoryID == nil || *user.InventoryID != 111 ||
		user.TradelistID == nil || *user.TradelistID != 222 ||
		user.WishlistID == nil || *user.WishlistID != 333 {
		t.Errorf("list ids = %+v, want 111/222/333", user)
	}
	if got := f.logins.Load(); got != 0 {
		t.Errorf("logins = %d, want none for a public page", got)
	}
}
