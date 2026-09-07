// Package admin serves the Admin Panel: a manage page for Deckbox Users and an
// analytics page for Demand and Trade Matches. It renders values the deckbox
// package produces and never touches storage itself. See docs/adr/0003.
package admin

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

// Service is what the panel needs from the domain. Declared here, in the
// consumer, so deckbox stays unaware it is being served over HTTP.
type Service interface {
	AdminOverview(ctx context.Context) (deckbox.Overview, error)
	AdminUsers(ctx context.Context) ([]deckbox.AdminUser, error)
	AdminInsights(ctx context.Context) (deckbox.Insights, error)
	Purge(ctx context.Context, login string) (deckbox.PurgeResult, error)
	Unlink(ctx context.Context, login string) error
	RefreshNow(ctx context.Context, login string) error
}

// Config is the panel's slice of the environment.
type Config struct {
	Addr     string
	User     string
	Password string
}

const (
	// insightsTTL is how long the analytics page is reused. Its queries scan
	// card_lists on the read pool that also serves live searches, so it is
	// computed on a timer rather than per request.
	insightsTTL = 5 * time.Minute

	// refreshTimeout bounds a panel-triggered Refresh: four Deckbox fetches,
	// the same budget registration gets.
	refreshTimeout = 2 * time.Minute

	// failedAuthDelay caps online password guessing without any per-IP state.
	failedAuthDelay = time.Second
)

// ErrNoPassword is returned when the panel is configured without a password.
// The panel refuses to start rather than exposing a delete endpoint openly.
var ErrNoPassword = errors.New("admin: ADMIN_PASSWORD is empty")

type Server struct {
	svc Service
	log *slog.Logger
	cfg Config

	mu       sync.Mutex
	insights *cachedInsights
}

type cachedInsights struct {
	data deckbox.Insights
	at   time.Time
}

// New builds the panel. It fails closed: an internet-reachable page that can
// purge every collection must never come up without a credential.
func New(log *slog.Logger, svc Service, cfg Config) (*Server, error) {
	if cfg.Password == "" {
		return nil, ErrNoPassword
	}
	if cfg.User == "" {
		cfg.User = "admin"
	}
	return &Server{svc: svc, log: log, cfg: cfg}, nil
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleManage)
	mux.HandleFunc("GET /insights", s.handleInsights)
	mux.HandleFunc("GET /purge", s.handleConfirmPurge)
	mux.HandleFunc("POST /purge", s.handlePurge)
	mux.HandleFunc("POST /unlink", s.handleUnlink)
	mux.HandleFunc("POST /refresh", s.handleRefresh)
	mux.HandleFunc("POST /add", s.handleAdd)
	return s.withAuth(mux)
}

// ListenAndServe runs the panel until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	const op = "admin.ListenAndServe"
	log := s.log.With(slog.String("operation", op), slog.String("addr", s.cfg.Addr))

	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Long enough for a panel-triggered refresh to finish inline.
		WriteTimeout: refreshTimeout + 30*time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Warn("admin panel shutdown", sl.Err(err))
		}
	}()

	log.Info("admin panel listening")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// withAuth applies Basic Auth to every route and same-origin checking to the
// mutating ones. Basic Auth makes CSRF worse than a SameSite cookie would — the
// browser attaches credentials to a cross-site POST automatically — so the
// origin check is what actually protects the delete endpoints.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			time.Sleep(failedAuthDelay)
			w.Header().Set("WWW-Authenticate", `Basic realm="FriendlyCardFinder admin", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			s.log.Warn("rejected cross-site admin post",
				slog.String("path", r.URL.Path),
				slog.String("origin", r.Header.Get("Origin")),
				slog.String("sec_fetch_site", r.Header.Get("Sec-Fetch-Site")))
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	// Both compares always run: bailing on the first mismatch would leak which
	// half was wrong through timing.
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.User)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Password)) == 1
	return userOK && passOK
}

// sameOrigin reports whether a mutating request came from the panel's own page.
// Every current browser sends Sec-Fetch-Site; Origin is the fallback. Neither
// present means the request did not come from a browser form, so it is refused.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host
	}
	return false
}

// --- actions -----------------------------------------------------------------

// handleConfirmPurge renders what a Purge would destroy. A GET has no side
// effect, so this is the confirmation step — and it keeps the pages free of the
// inline script a browser confirm() dialog would need.
func (s *Server) handleConfirmPurge(w http.ResponseWriter, r *http.Request) {
	const op = "admin.handleConfirmPurge"
	log := s.log.With(slog.String("operation", op))

	login := strings.TrimSpace(r.URL.Query().Get("login"))
	if login == "" {
		s.redirect(w, r, "no login given")
		return
	}

	users, err := s.svc.AdminUsers(r.Context())
	if err != nil {
		log.Error("failed to load users", sl.Err(err))
		http.Error(w, "failed to load users", http.StatusInternalServerError)
		return
	}

	view, ok := buildConfirmView(users, login)
	if !ok {
		s.redirect(w, r, "unknown login: "+login)
		return
	}

	s.render(w, log, confirmTemplate, view)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSpace(r.FormValue("login"))
	if login == "" {
		s.redirect(w, r, "no login given")
		return
	}

	res, err := s.svc.Purge(r.Context(), login)
	if err != nil {
		s.redirect(w, r, fmt.Sprintf("purge %s failed: %v", login, err))
		return
	}

	msg := fmt.Sprintf("purged %s — %d lists, %d card rows", login, len(res.ListIDs), res.CardRows)
	if res.Registration {
		msg += ", registration removed"
	}
	s.invalidate()
	s.redirect(w, r, msg)
}

func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSpace(r.FormValue("login"))
	if login == "" {
		s.redirect(w, r, "no login given")
		return
	}

	if err := s.svc.Unlink(r.Context(), login); err != nil {
		s.redirect(w, r, fmt.Sprintf("unlink %s failed: %v", login, err))
		return
	}
	s.redirect(w, r, "unlinked registration for "+login)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	s.refresh(w, r, strings.TrimSpace(r.FormValue("login")), "refresh", "refreshed")
}

// handleAdd is the same operation as refresh — a Refresh of an unknown login
// creates the Deckbox User — so it shares the path and differs only in wording.
func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	s.refresh(w, r, strings.TrimSpace(r.FormValue("login")), "add", "added")
}

// refresh reports the action twice over: "refresh petya failed: ..." while it is
// happening, "refreshed petya" once it has.
func (s *Server) refresh(w http.ResponseWriter, r *http.Request, login, present, past string) {
	if login == "" {
		s.redirect(w, r, "no login given")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()

	if err := s.svc.RefreshNow(ctx, login); err != nil {
		s.redirect(w, r, fmt.Sprintf("%s %s failed: %v", present, login, err))
		return
	}
	s.invalidate()
	s.redirect(w, r, past+" "+login)
}

// redirect implements POST-then-redirect so a reload never repeats an action.
// The message rides in the query string; html/template escapes it on the way out.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// invalidate drops the cached analytics after an action that changes them, so
// the numbers do not contradict the manage page for the next five minutes.
func (s *Server) invalidate() {
	s.mu.Lock()
	s.insights = nil
	s.mu.Unlock()
}

// --- pages -------------------------------------------------------------------

func (s *Server) handleManage(w http.ResponseWriter, r *http.Request) {
	const op = "admin.handleManage"
	log := s.log.With(slog.String("operation", op))

	overview, err := s.svc.AdminOverview(r.Context())
	if err != nil {
		log.Error("failed to load overview", sl.Err(err))
		http.Error(w, "failed to load overview", http.StatusInternalServerError)
		return
	}

	users, err := s.svc.AdminUsers(r.Context())
	if err != nil {
		log.Error("failed to load users", sl.Err(err))
		http.Error(w, "failed to load users", http.StatusInternalServerError)
		return
	}

	s.render(w, log, manageTemplate, buildManageView(overview, users, r.URL.Query().Get("msg")))
}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	const op = "admin.handleInsights"
	log := s.log.With(slog.String("operation", op))

	if r.URL.Query().Get("refresh") != "" {
		s.invalidate()
	}

	data, computedAt, err := s.cachedInsightsFor(r.Context())
	if err != nil {
		log.Error("failed to load insights", sl.Err(err))
		http.Error(w, "failed to load insights", http.StatusInternalServerError)
		return
	}

	s.render(w, log, insightsTemplate, buildInsightsView(data, computedAt))
}

// cachedInsightsFor memoizes the analytics for insightsTTL. The lock is held
// across the queries on purpose: a second concurrent request waits for the
// first rather than starting its own scan of card_lists.
func (s *Server) cachedInsightsFor(ctx context.Context) (deckbox.Insights, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.insights != nil && time.Since(s.insights.at) < insightsTTL {
		return s.insights.data, s.insights.at, nil
	}

	data, err := s.svc.AdminInsights(ctx)
	if err != nil {
		return deckbox.Insights{}, time.Time{}, err
	}

	s.insights = &cachedInsights{data: data, at: time.Now()}
	return data, s.insights.at, nil
}

func (s *Server) render(w http.ResponseWriter, log *slog.Logger, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The panel serves no third-party anything; the CSP makes that explicit.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	w.Header().Set("Referrer-Policy", "same-origin")

	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		// Too late for an error page — the status and part of the body are out.
		log.Error("failed to render admin page", sl.Err(err))
	}
}
