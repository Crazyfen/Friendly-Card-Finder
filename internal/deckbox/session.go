package deckbox

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// session resolves and caches the Deckbox `_tcg_session` cookie. It owns the
// policy — where a cookie may come from, when to log in again, and how many
// logins a burst of refresh workers is allowed to cause — and knows nothing
// about HTTP: the login arrives as a function, so a test can supply one that
// counts its calls instead of dialing deckbox.org.
type session struct {
	override string // DECKBOX_SESSION_COOKIE; used verbatim when set
	path     string // file the obtained cookie is persisted to
	login    func(context.Context) (string, error)
	log      *slog.Logger

	mu     sync.Mutex // guards cookie and serializes logins (single-flight)
	cookie string
}

func newSession(log *slog.Logger, override, path string, login func(context.Context) (string, error)) *session {
	return &session{override: override, path: path, login: login, log: log}
}

// Cookie returns a usable cookie, resolving in priority order: in-memory cache,
// env override, persisted file, then a fresh login.
func (s *session) Cookie(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cookie != "" {
		return s.cookie, nil
	}
	if s.override != "" {
		s.cookie = s.override
		return s.cookie, nil
	}
	if c := readCookieFile(s.path); c != "" {
		s.cookie = c
		return s.cookie, nil
	}
	return s.loginLocked(ctx)
}

// Refresh logs in again after previous was rejected. When another goroutine has
// already replaced it, that newer cookie is returned instead: the worker pools
// all hit an expired cookie within the same second, and one rejection must cause
// one login, not ten.
func (s *session) Refresh(ctx context.Context, previous string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cookie != "" && s.cookie != previous {
		return s.cookie, nil
	}
	return s.loginLocked(ctx)
}

// loginLocked logs in, then caches and persists the cookie. The caller must hold
// s.mu, which is what makes the login single-flight.
func (s *session) loginLocked(ctx context.Context) (string, error) {
	cookie, err := s.login(ctx)
	if err != nil {
		return "", err
	}
	s.cookie = cookie
	if err := writeCookieFile(s.path, cookie); err != nil {
		s.log.Warn("failed to persist session cookie", slog.String("error", err.Error()))
	}
	return cookie, nil
}

// readCookieFile returns the persisted cookie value, or "" if the file is
// missing/unreadable. A missing file is the normal first-run case.
func readCookieFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeCookieFile persists the session cookie with owner-only permissions
// (it is a secret).
func writeCookieFile(path, cookie string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(cookie), 0o600)
}
