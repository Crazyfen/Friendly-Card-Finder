package deckbox

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// buildExportBody synthesizes a Deckbox export-page body: "<qty> <card name>"
// entries joined by <br/>, with a couple of HTML-escaped names mixed in so the
// html.UnescapeString cost is represented.
func buildExportBody(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0:
			fmt.Fprintf(&b, "%d Lightning Bolt", i%4+1)
		case 1:
			fmt.Fprintf(&b, "%d &quot;Brims&quot; Barone, Midway Mobster", i%4+1)
		default:
			fmt.Fprintf(&b, "%d Sm&eacute;agol, Helpful Guide", i%4+1)
		}
		b.WriteString("<br/>")
	}
	return b.String()
}

func TestParseAuthenticityToken(t *testing.T) {
	// Mirrors the real Deckbox login form, including the quirky `value ='...'`
	// spacing and single-quoted attributes.
	const loginHTML = `<html><body>
		<form action="/accounts/login" method="POST" autocomplete="on">
		  <input type='hidden' name='authenticity_token' value ='tok-abc123' />
		  <input type="hidden" name="return_to" value="" />
		  <input type="text" name="login" id="login" />
		  <input type="password" name="password" id="password" />
		</form></body></html>`

	got, err := parseAuthenticityToken(loginHTML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tok-abc123" {
		t.Errorf("expected token 'tok-abc123', got %q", got)
	}

	if _, err := parseAuthenticityToken("<html><body>no form here</body></html>"); err == nil {
		t.Error("expected error when authenticity_token is absent, got nil")
	}
}

func TestCookieFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deckbox_session")

	// Missing file reads as empty.
	if got := readCookieFile(path); got != "" {
		t.Errorf("expected empty read for missing file, got %q", got)
	}

	const cookie = "abc%2Fdef--xyz%3D%3D"
	if err := writeCookieFile(path, cookie); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if got := readCookieFile(path); got != cookie {
		t.Errorf("round-trip mismatch: wrote %q, read %q", cookie, got)
	}

	// Empty path is a no-op (persistence disabled).
	if err := writeCookieFile("", cookie); err != nil {
		t.Errorf("expected nil error for empty path, got %v", err)
	}
	if got := readCookieFile(""); got != "" {
		t.Errorf("expected empty read for empty path, got %q", got)
	}
}

func BenchmarkParseCardListExport(b *testing.B) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, n := range []int{100, 1000, 10000} {
		body := buildExportBody(n)
		b.Run(fmt.Sprintf("cards=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				if got := parseCardListExport(body, log); len(got) == 0 {
					b.Fatal("parsed empty card list")
				}
			}
		})
	}
}