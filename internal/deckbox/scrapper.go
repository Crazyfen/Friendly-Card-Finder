package deckbox

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly"
)

const sessionCookieName = "_tcg_session"

func (s *Scraper) FetchDeckboxUserProfile(ctx context.Context, deckboxLogin string) (DeckboxUser, error) {
	const op = "scrapper.FetchDeckboxUserProfile"
	log := s.log.With(slog.String("operation", op), slog.String("deckbox_id", deckboxLogin))

	c := colly.NewCollector(
		colly.AllowedDomains("deckbox.org", "www.deckbox.org"),
	)

	user := DeckboxUser{
		DeckboxLogin: deckboxLogin,
		InventoryID:  nil,
		TradelistID:  nil,
		WishlistID:   nil,
	}

	c.OnHTML("#section_mtg > .submenu_entry", func(e *colly.HTMLElement) {
		log.Info("found submenu entry", slog.String("text", e.Text))
		if class := e.Attr("class"); class != "" {
			if strings.Contains(class, "t_wish") {
				user.WishlistID, _ = extractIDFromElement(e)
			} else if strings.Contains(class, "t_inv") {
				user.InventoryID, _ = extractIDFromElement(e)
			} else {
				user.TradelistID, _ = extractIDFromElement(e)
			}
		}
	})

	// Retry on transient errors with exponential backoff
	var err error
	const maxAttempts = 3
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return DeckboxUser{}, ctx.Err()
		default:
		}

		err = c.Visit(ProfileURL + deckboxLogin)
		if err == nil {
			break
		}
		log.Warn("visit profile failed, retrying", slog.String("deckbox_id", deckboxLogin), slog.Int("attempt", i+1), slog.String("error", err.Error()))

		select {
		case <-ctx.Done():
			return DeckboxUser{}, ctx.Err()
		case <-time.After(time.Duration(200*(1<<i)) * time.Millisecond):
		}
	}
	if err != nil {
		log.Error("failed to visit profile page", slog.String("deckbox_id", deckboxLogin), slog.String("error", err.Error()))
		return DeckboxUser{}, err
	}

	return user, nil
}

func (s *Scraper) FetchCardList(ctx context.Context, listId int64) (CardList, error) {
	const op = "scrapper.FetchCardList"
	log := s.log.With(slog.String("operation", op), slog.Int64("list_id", listId))

	cookie, err := s.cookie(ctx)
	if err != nil {
		return CardList{}, fmt.Errorf("%s: %w", op, err)
	}

	cardList, authFailed, err := s.fetchCardListOnce(ctx, listId, cookie, log)
	if err != nil {
		return CardList{}, err
	}

	// The session cookie was rejected (Deckbox served the login page). Re-login
	// once and retry, so an expired cookie self-heals without manual intervention.
	if authFailed {
		log.Warn("card list export looks unauthenticated, re-logging in")
		cookie, err = s.refreshCookie(ctx, cookie)
		if err != nil {
			return CardList{}, fmt.Errorf("%s: %w", op, err)
		}
		cardList, authFailed, err = s.fetchCardListOnce(ctx, listId, cookie, log)
		if err != nil {
			return CardList{}, err
		}
		if authFailed {
			return CardList{}, fmt.Errorf("%s: still unauthenticated after re-login", op)
		}
	}

	return cardList, nil
}

// fetchCardListOnce performs a single export fetch with the given cookie. It
// returns authFailed=true when the response was the login page rather than the
// export (an invalid/expired cookie), so the caller can re-login and retry.
func (s *Scraper) fetchCardListOnce(ctx context.Context, listId int64, cookie string, log *slog.Logger) (CardList, bool, error) {
	c := colly.NewCollector(
		colly.AllowedDomains("deckbox.org", "www.deckbox.org"),
	)

	c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"

	c.OnRequest(func(r *colly.Request) {
		c.SetCookies(r.URL.String(), []*http.Cookie{
			{
				Name:   sessionCookieName,
				Value:  cookie,
				Domain: r.URL.Host,
				Path:   "/",
			},
		})
	})

	cardList := CardList{
		ListId: listId,
		Cards:  make(map[string]int16),
	}
	var authFailed bool

	c.OnResponse(func(r *colly.Response) {
		// An invalid cookie makes Deckbox redirect to the login page; colly
		// follows redirects, so the final URL lands on /accounts/login.
		if strings.HasSuffix(r.Request.URL.Path, "/accounts/login") {
			authFailed = true
		}
	})

	c.OnHTML("body", func(e *colly.HTMLElement) {
		body, _ := e.DOM.Html()
		// Belt-and-suspenders: detect the login form in the body too.
		if strings.Contains(body, "name='authenticity_token'") || strings.Contains(body, `name="authenticity_token"`) {
			authFailed = true
			return
		}
		for cardName, quantity := range parseCardListExport(body, log) {
			cardList.AddCard(cardName, quantity)
		}
	})

	// Retry with a backoff for transient network errors
	var err error
	const maxAttempts = 3
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return CardList{}, false, ctx.Err()
		default:
		}

		err = c.Visit(ExportURL + fmt.Sprintf("%d", listId) + "/export")
		if err == nil {
			break
		}
		log.Warn("visit export failed, retrying", slog.Int("attempt", i+1), slog.String("error", err.Error()))

		select {
		case <-ctx.Done():
			return CardList{}, false, ctx.Err()
		case <-time.After(time.Duration(200*(1<<i)) * time.Millisecond):
		}
	}
	if err != nil {
		log.Error("failed to visit card list page", slog.Int64("list_id", listId), slog.String("error", err.Error()))
		return CardList{}, false, err
	}

	return cardList, authFailed, nil
}

// cookie returns a usable _tcg_session value, resolving in priority order:
// in-memory cache, env override, persisted file, then a fresh login.
func (s *Scraper) cookie(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionCookie != "" {
		return s.sessionCookie, nil
	}
	if s.cookieOverride != "" {
		s.sessionCookie = s.cookieOverride
		return s.sessionCookie, nil
	}
	if c := readCookieFile(s.cookiePath); c != "" {
		s.sessionCookie = c
		return s.sessionCookie, nil
	}
	return s.loginLocked(ctx)
}

// refreshCookie forces a re-login when the cached cookie was rejected. previous
// is the cookie the caller just tried; if another goroutine already refreshed it
// in the meantime, that newer value is returned without a redundant login.
func (s *Scraper) refreshCookie(ctx context.Context, previous string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionCookie != "" && s.sessionCookie != previous {
		return s.sessionCookie, nil
	}
	if s.login == "" || s.password == "" {
		return "", fmt.Errorf("session cookie rejected and no credentials configured to re-login")
	}
	return s.loginLocked(ctx)
}

// loginLocked performs a login, then caches and persists the cookie.
// The caller must hold s.mu.
func (s *Scraper) loginLocked(ctx context.Context) (string, error) {
	if s.login == "" || s.password == "" {
		return "", fmt.Errorf("no deckbox credentials configured")
	}
	cookie, err := s.doLogin(ctx)
	if err != nil {
		return "", err
	}
	s.sessionCookie = cookie
	if err := writeCookieFile(s.cookiePath, cookie); err != nil {
		s.log.Warn("failed to persist session cookie", slog.String("error", err.Error()))
	}
	return cookie, nil
}

// doLogin authenticates against Deckbox and returns the _tcg_session cookie value.
// It GETs the login page (capturing the CSRF-bound session cookie in the jar and
// the authenticity_token from the form), then POSTs the credentials.
func (s *Scraper) doLogin(ctx context.Context) (string, error) {
	const op = "scrapper.doLogin"
	log := s.log.With(slog.String("operation", op))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LoginURL, nil)
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: get login page: %w", op, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("%s: read login page: %w", op, err)
	}

	token, err := parseAuthenticityToken(string(body))
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}

	form := url.Values{
		"authenticity_token": {token},
		"return_to":          {""},
		"login":              {s.login},
		"password":           {s.password},
	}
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, LoginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	postResp, err := s.httpClient.Do(postReq)
	if err != nil {
		return "", fmt.Errorf("%s: post login: %w", op, err)
	}
	io.Copy(io.Discard, postResp.Body)
	postResp.Body.Close()

	// Success is a redirect (3xx); a 2xx means the login form was re-rendered.
	if postResp.StatusCode < 300 || postResp.StatusCode >= 400 {
		return "", fmt.Errorf("%s: login failed (status %d) — check DECKBOX_LOGIN/DECKBOX_PASSWORD", op, postResp.StatusCode)
	}

	u, err := url.Parse(DeckboxBaseURL)
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}
	for _, c := range s.httpClient.Jar.Cookies(u) {
		if c.Name == sessionCookieName && c.Value != "" {
			log.Info("deckbox login successful")
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("%s: login succeeded but no %s cookie returned", op, sessionCookieName)
}

// parseAuthenticityToken extracts the Rails CSRF token from the login page HTML.
func parseAuthenticityToken(body string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("parse login html: %w", err)
	}
	token, ok := doc.Find("input[name='authenticity_token']").First().Attr("value")
	if !ok || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("authenticity_token not found in login page")
	}
	return strings.TrimSpace(token), nil
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

// parseCardListExport parses the raw HTML body of a Deckbox set export page into
// a map of card name -> quantity. The export format is "<qty> <card name>"
// entries separated by <br/> tags. Quantities for duplicate names are summed;
// malformed entries are logged and skipped.
func parseCardListExport(body string, log *slog.Logger) map[string]int16 {
	cards := make(map[string]int16)
	body = strings.ReplaceAll(body, "\n", "")
	for element := range strings.SplitSeq(body, "<br/>") {
		element = strings.TrimSpace(element)
		if element == "" {
			continue
		}
		element = html.UnescapeString(element)
		parts := strings.SplitN(element, " ", 2)
		if len(parts) != 2 {
			log.Error("failed to parse element", slog.String("element", element))
			continue
		}
		quantity, err := strconv.Atoi(parts[0])
		if err != nil {
			log.Error("failed to parse quantity", slog.String("element", element), slog.String("error", err.Error()))
			continue
		}
		cards[parts[1]] += int16(quantity)
	}
	return cards
}

func extractIDFromElement(e *colly.HTMLElement) (*int64, error) {
	url := e.ChildAttr("a", "href")
	var id int64
	_, err := fmt.Sscanf(url, "/sets/%d", &id)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
