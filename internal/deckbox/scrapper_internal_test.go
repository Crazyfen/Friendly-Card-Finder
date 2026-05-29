package deckbox

import (
	"fmt"
	"io"
	"log/slog"
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