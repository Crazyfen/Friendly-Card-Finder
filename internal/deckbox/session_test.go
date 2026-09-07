package deckbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// countingLogin stands in for the HTTP login. Counting its calls is the whole
// point: the rules under test are about how many logins a burst of refresh
// workers is allowed to cause.
type countingLogin struct {
	calls atomic.Int64
	err   error
}

// fn numbers every cookie it issues, so a test can tell a fresh login from a
// cached one by the value alone.
func (c *countingLogin) fn(ctx context.Context) (string, error) {
	n := c.calls.Add(1)
	if c.err != nil {
		return "", c.err
	}
	return fmt.Sprintf("cookie-%d", n), nil
}

func newTestSession(t *testing.T, override string, login *countingLogin) *session {
	t.Helper()
	return newSession(quietLog(), override, filepath.Join(t.TempDir(), "deckbox_session"), login.fn)
}

func TestSessionResolutionOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("the override is used verbatim and never logs in", func(t *testing.T) {
		login := &countingLogin{}
		s := newTestSession(t, "manual-cookie", login)

		got, err := s.Cookie(ctx)
		if err != nil {
			t.Fatalf("cookie failed: %v", err)
		}
		if got != "manual-cookie" {
			t.Errorf("cookie = %q, want the override", got)
		}
		if login.calls.Load() != 0 {
			t.Error("an override must not cause a login")
		}
	})

	t.Run("a persisted cookie beats logging in again", func(t *testing.T) {
		login := &countingLogin{}
		s := newTestSession(t, "", login)
		if err := os.WriteFile(s.path, []byte("  saved-cookie\n"), 0o600); err != nil {
			t.Fatalf("seed cookie file: %v", err)
		}

		got, err := s.Cookie(ctx)
		if err != nil {
			t.Fatalf("cookie failed: %v", err)
		}
		if got != "saved-cookie" {
			t.Errorf("cookie = %q, want the persisted value, trimmed", got)
		}
		if login.calls.Load() != 0 {
			t.Error("a restart with a good cookie on disk must not log in")
		}
	})

	t.Run("with nothing to go on it logs in and persists", func(t *testing.T) {
		login := &countingLogin{}
		s := newTestSession(t, "", login)

		got, err := s.Cookie(ctx)
		if err != nil {
			t.Fatalf("cookie failed: %v", err)
		}
		if got != "cookie-1" || login.calls.Load() != 1 {
			t.Fatalf("cookie = %q after %d logins, want one login", got, login.calls.Load())
		}

		// Persisting is what lets the next process start without a login.
		saved, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatalf("cookie was not persisted: %v", err)
		}
		if string(saved) != "cookie-1" {
			t.Errorf("persisted %q, want the new cookie", saved)
		}
	})

	t.Run("a login failure is reported, not cached", func(t *testing.T) {
		login := &countingLogin{err: errors.New("no deckbox credentials configured")}
		s := newTestSession(t, "", login)

		if _, err := s.Cookie(ctx); err == nil {
			t.Fatal("expected the login error to reach the caller")
		}
		if _, err := s.Cookie(ctx); err == nil {
			t.Fatal("expected the second attempt to try again, not serve a cached failure")
		}
		if login.calls.Load() != 2 {
			t.Errorf("expected each attempt to retry the login, got %d", login.calls.Load())
		}
	})
}

func TestSessionLogsInOncePerBurst(t *testing.T) {
	// RefreshStale runs ten workers issuing four requests each. On a cold start
	// they all want a cookie at the same moment; without single-flight that is
	// ten logins against deckbox.org for one credential.
	login := &countingLogin{}
	s := newTestSession(t, "", login)

	var wg sync.WaitGroup
	cookies := make([]string, 20)
	for i := range cookies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := s.Cookie(context.Background())
			if err != nil {
				t.Errorf("cookie failed: %v", err)
				return
			}
			cookies[i] = c
		}(i)
	}
	wg.Wait()

	if got := login.calls.Load(); got != 1 {
		t.Errorf("20 concurrent callers caused %d logins, want 1", got)
	}
	for i, c := range cookies {
		if c != "cookie-1" {
			t.Fatalf("caller %d got %q, want every caller to share one cookie", i, c)
		}
	}
}

func TestSessionRefresh(t *testing.T) {
	ctx := context.Background()

	t.Run("a rejected cookie is replaced", func(t *testing.T) {
		login := &countingLogin{}
		s := newTestSession(t, "", login)

		stale, err := s.Cookie(ctx)
		if err != nil {
			t.Fatalf("cookie failed: %v", err)
		}

		fresh, err := s.Refresh(ctx, stale)
		if err != nil {
			t.Fatalf("refresh failed: %v", err)
		}
		if fresh != "cookie-2" {
			t.Errorf("refresh returned %q, want a new cookie", fresh)
		}
		if login.calls.Load() != 2 {
			t.Errorf("expected exactly one extra login, got %d total", login.calls.Load())
		}
	})

	t.Run("a cookie someone else already replaced causes no login", func(t *testing.T) {
		// Ten workers hit the same expired cookie within a second of each other.
		// The first refreshes it; the rest must take the new one and move on.
		login := &countingLogin{}
		s := newTestSession(t, "", login)
		s.cookie = "already-refreshed"

		got, err := s.Refresh(ctx, "the-stale-one")
		if err != nil {
			t.Fatalf("refresh failed: %v", err)
		}
		if got != "already-refreshed" {
			t.Errorf("refresh returned %q, want the cookie another worker just fetched", got)
		}
		if login.calls.Load() != 0 {
			t.Error("a cookie that was already refreshed must not cause a second login")
		}
	})
}
