package deckbox

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
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
)

const sessionCookieName = "_tcg_session"

const scraperUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"

// fetchPage GETs a Deckbox page with retries and exponential backoff for
// transient errors, returning the response body and the final URL after any
// redirects (used to detect a bounce to the login page). All scrape fetches go
// through the shared fetchClient so keep-alive connections are reused.
func (s *Scraper) fetchPage(ctx context.Context, pageURL, cookie string, log *slog.Logger) (body []byte, finalURL *url.URL, err error) {
	const maxAttempts = 3
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		body, finalURL, err = s.fetchPageOnce(ctx, pageURL, cookie)
		if err == nil {
			return body, finalURL, nil
		}
		log.Warn("fetch failed, retrying", slog.String("url", pageURL), slog.Int("attempt", i+1), slog.String("error", err.Error()))

		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Duration(200*(1<<i)) * time.Millisecond):
		}
	}
	return nil, nil, err
}

func (s *Scraper) fetchPageOnce(ctx context.Context, pageURL, cookie string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", scraperUserAgent)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}

	resp, err := s.fetchClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, pageURL)
	}
	// resp.Request reflects the final request after redirects.
	return body, resp.Request.URL, nil
}

func (s *Scraper) FetchDeckboxUserProfile(ctx context.Context, deckboxLogin string) (DeckboxUser, error) {
	const op = "scrapper.FetchDeckboxUserProfile"
	log := s.log.With(slog.String("operation", op), slog.String("deckbox_id", deckboxLogin))

	body, _, err := s.fetchPage(ctx, ProfileURL+deckboxLogin, "", log)
	if err != nil {
		log.Error("failed to visit profile page", slog.String("error", err.Error()))
		return DeckboxUser{}, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		log.Error("failed to parse profile page", slog.String("error", err.Error()))
		return DeckboxUser{}, err
	}

	user := DeckboxUser{
		DeckboxLogin: deckboxLogin,
		InventoryID:  nil,
		TradelistID:  nil,
		WishlistID:   nil,
	}

	doc.Find("#section_mtg > .submenu_entry").Each(func(_ int, sel *goquery.Selection) {
		class, _ := sel.Attr("class")
		if class == "" {
			return
		}
		href, _ := sel.Find("a").First().Attr("href")
		if strings.Contains(class, "t_wish") {
			user.WishlistID, _ = extractIDFromHref(href)
		} else if strings.Contains(class, "t_inv") {
			user.InventoryID, _ = extractIDFromHref(href)
		} else {
			user.TradelistID, _ = extractIDFromHref(href)
		}
	})

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
	exportURL := ExportURL + strconv.FormatInt(listId, 10) + "/export"

	rawBody, finalURL, err := s.fetchPage(ctx, exportURL, cookie, log)
	if err != nil {
		log.Error("failed to visit card list page", slog.String("error", err.Error()))
		return CardList{}, false, err
	}

	// An invalid cookie makes Deckbox redirect to the login page; the fetch
	// client follows redirects, so the final URL lands on /accounts/login.
	if strings.HasSuffix(finalURL.Path, "/accounts/login") {
		return CardList{}, true, nil
	}

	// The export is plain text ("<qty> <name>" joined by <br/>), so parse the
	// raw response body directly without building a DOM.
	body := bodyInnerHTML(string(rawBody))
	// Belt-and-suspenders: detect the login form in the body too.
	if strings.Contains(body, "name='authenticity_token'") || strings.Contains(body, `name="authenticity_token"`) {
		return CardList{}, true, nil
	}

	h := fnv.New64a()
	h.Write([]byte(body))

	return CardList{
		ListId:   listId,
		Cards:    parseCardListExport(body, log),
		BodyHash: h.Sum64(),
	}, false, nil
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

// bodyInnerHTML returns the markup between the opening <body ...> tag and the
// closing </body>, matching what colly's goquery OnHTML("body") handler used to
// hand us — but with cheap string slicing instead of building and re-serializing
// a full DOM. Deckbox (Rails) emits lowercase tags; if no <body> is present the
// input is returned unchanged so parsing still has something to work with.
func bodyInnerHTML(s string) string {
	open := strings.Index(s, "<body")
	if open == -1 {
		return s
	}
	contentStart := open + len("<body")
	// Skip to the end of the opening tag, past any attributes.
	if gt := strings.IndexByte(s[contentStart:], '>'); gt != -1 {
		contentStart += gt + 1
	}
	closeIdx := strings.LastIndex(s, "</body>")
	if closeIdx < contentStart {
		return s[contentStart:]
	}
	return s[contentStart:closeIdx]
}

// parseCardListExport parses the raw HTML body of a Deckbox set export page into
// a map of card name -> quantity. The export format is "<qty> <card name>"
// entries separated by <br/> tags. Quantities for duplicate names are summed;
// malformed entries are logged and skipped.
func parseCardListExport(body string, log *slog.Logger) map[string]int16 {
	// Pre-size to the entry count so a 10k-card export doesn't rehash the map
	// ~14 times while growing; one extra O(n) scan is far cheaper.
	cards := make(map[string]int16, strings.Count(body, "<br/>")+1)
	// TrimSpace on each element below also strips the stray newlines that the
	// source formats into the export, so no whole-body copy is needed here.
	for element := range strings.SplitSeq(body, "<br/>") {
		element = strings.TrimSpace(element)
		if element == "" {
			continue
		}
		element = html.UnescapeString(element)
		// Split on the first space without allocating a slice (SplitN would).
		sp := strings.IndexByte(element, ' ')
		if sp <= 0 {
			log.Error("failed to parse element", slog.String("element", element))
			continue
		}
		quantity, err := strconv.Atoi(element[:sp])
		if err != nil {
			log.Error("failed to parse quantity", slog.String("element", element), slog.String("error", err.Error()))
			continue
		}
		cards[element[sp+1:]] += int16(quantity)
	}
	return cards
}

// extractIDFromHref parses a list href of the form "/sets/<id>" into the id.
func extractIDFromHref(href string) (*int64, error) {
	var id int64
	_, err := fmt.Sscanf(href, "/sets/%d", &id)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
