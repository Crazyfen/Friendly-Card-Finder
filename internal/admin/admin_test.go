package admin

import (
	"FriendlyCardFinder/internal/deckbox"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeService records what the panel asked the domain to do.
type fakeService struct {
	purged []string
	users  []deckbox.AdminUser
}

func (f *fakeService) AdminOverview(ctx context.Context) (deckbox.Overview, error) {
	return deckbox.Overview{BotUsers: len(f.users)}, nil
}
func (f *fakeService) AdminUsers(ctx context.Context) ([]deckbox.AdminUser, error) {
	return f.users, nil
}
func (f *fakeService) AdminInsights(ctx context.Context) (deckbox.Insights, error) {
	return deckbox.Insights{}, nil
}
func (f *fakeService) Purge(ctx context.Context, login string) (deckbox.PurgeResult, error) {
	f.purged = append(f.purged, login)
	return deckbox.PurgeResult{ListIDs: []int64{1, 2, 3}, CardRows: 12, Registration: true}, nil
}
func (f *fakeService) Unlink(ctx context.Context, login string) error     { return nil }
func (f *fakeService) RefreshNow(ctx context.Context, login string) error { return nil }

func newTestServer(t *testing.T, svc Service) http.Handler {
	t.Helper()
	s, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), svc, Config{
		Addr: ":0", User: "admin", Password: "hunter2",
	})
	if err != nil {
		t.Fatalf("failed to build panel: %v", err)
	}
	return s.Handler()
}

func TestPanelRefusesToStartWithoutPassword(t *testing.T) {
	// The panel is internet-reachable and can purge every collection, so a
	// missing password must disable it rather than open it.
	_, err := New(slog.Default(), &fakeService{}, Config{Addr: ":8081"})
	if !errors.Is(err, ErrNoPassword) {
		t.Fatalf("expected ErrNoPassword, got %v", err)
	}
}

func TestUnauthenticatedRequestIsChallenged(t *testing.T) {
	svc := &fakeService{}
	h := newTestServer(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/purge", strings.NewReader("login=petya"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected a Basic auth challenge header")
	}
	if len(svc.purged) != 0 {
		t.Fatalf("an unauthenticated request reached Purge: %v", svc.purged)
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	h := newTestServer(t, &fakeService{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "hunter3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for a wrong password, got %d", rec.Code)
	}
}

func TestCrossSitePostRejected(t *testing.T) {
	// Basic Auth means the browser attaches credentials to a cross-site POST by
	// itself, so authentication alone would not stop another site from posting
	// a delete. The origin check is what does.
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"cross-site fetch metadata", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"same-site but not same-origin", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}},
		{"no origin signal at all", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			h := newTestServer(t, svc)

			req := httptest.NewRequest(http.MethodPost, "/purge", strings.NewReader("login=petya"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetBasicAuth("admin", "hunter2")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("expected 403, got %d", rec.Code)
			}
			if len(svc.purged) != 0 {
				t.Fatalf("a cross-site POST reached Purge: %v", svc.purged)
			}
		})
	}
}

func TestSameOriginPostPurges(t *testing.T) {
	svc := &fakeService{}
	h := newTestServer(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/purge", strings.NewReader("login=petya"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.SetBasicAuth("admin", "hunter2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect after the action, got %d", rec.Code)
	}
	if len(svc.purged) != 1 || svc.purged[0] != "petya" {
		t.Fatalf("expected petya purged, got %v", svc.purged)
	}

	// The outcome rides back in the query string and must survive escaping.
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad redirect location: %v", err)
	}
	if !strings.Contains(loc.Query().Get("msg"), "purged petya") {
		t.Errorf("expected the redirect to report the purge, got %q", loc.RawQuery)
	}
}

func TestPurgeIsAGetConfirmationFirst(t *testing.T) {
	// GET /purge must only describe the purge; a browser prefetch or a stray
	// link must never delete anything.
	login := "petya"
	tgID := int64(7)
	svc := &fakeService{users: []deckbox.AdminUser{{
		DeckboxLogin: login, TelegramID: &tgID,
		InventoryCount: 100, TradelistCount: 200, WishlistCount: 3,
	}}}
	h := newTestServer(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/purge?login=petya", nil)
	req.SetBasicAuth("admin", "hunter2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected the confirmation page, got %d", rec.Code)
	}
	if len(svc.purged) != 0 {
		t.Fatalf("GET /purge deleted something: %v", svc.purged)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Purge petya?") {
		t.Errorf("confirmation page does not name the login: %q", firstLines(body))
	}
}

func TestPagesRender(t *testing.T) {
	// Template execution errors surface at render time, not compile time, so
	// both pages get walked once with data in them.
	tgName := "petya_tg"
	tgID := int64(7)
	svc := &fakeService{users: []deckbox.AdminUser{{
		DeckboxLogin: "petya", TelegramID: &tgID, TelegramUsername: &tgName,
		InventoryCount: 1000, TradelistCount: 2000, WishlistCount: 0,
		MissingLists: 1, LastError: "failed to fetch profile",
	}}}
	h := newTestServer(t, svc)

	for _, path := range []string{"/", "/insights"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetBasicAuth("admin", "hunter2")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "</html>") {
			t.Errorf("%s: page did not render to completion: %q", path, firstLines(rec.Body.String()))
		}
	}
}

func firstLines(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
