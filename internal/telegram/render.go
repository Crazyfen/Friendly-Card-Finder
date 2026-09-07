// Package telegram turns Telegram messages into replies: the commands in
// command.go decide what to say, and the renderers here decide how a search
// result looks. Everything that knows about Telegram markup or user-facing
// wording lives here, so the deckbox module deals only in domain values.
package telegram

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	b64 "encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// messageKeys are the i18n keys for one search scope. Tradelist and wishlist
// searches render identically and differ only in wording.
type messageKeys struct {
	noResults      string
	resultsHeader  string
	multiNoResults string
	multiHeader    string
	multiNotFound  string
}

func keysFor(scope deckbox.Scope) messageKeys {
	if scope == deckbox.ScopeWishlist {
		return messageKeys{
			noResults:      "sell.no_results",
			resultsHeader:  "sell.results_header",
			multiNoResults: "sell.multi_no_results",
			multiHeader:    "sell.multi_results_header",
			multiNotFound:  "sell.multi_not_found",
		}
	}
	return messageKeys{
		noResults:      "search.no_results",
		resultsHeader:  "search.results_header",
		multiNoResults: "search.multi_no_results",
		multiHeader:    "search.multi_results_header",
		multiNotFound:  "search.multi_not_found",
	}
}

// RenderSearch returns (mainMessage, notFoundMessage). The second string is
// empty when every queried card was found at least once, and for a single-card
// search, where the main message already says so.
func RenderSearch(r deckbox.SearchResult, lang string, scope deckbox.Scope) (string, string) {
	if len(r.SearchQueries) == 1 {
		return renderSingle(r, lang, scope), ""
	}
	return renderMulti(r, lang, scope)
}

// renderSingle lists every matching card per owner, with a Deckbox link
// pre-filtered to the query.
func renderSingle(r deckbox.SearchResult, lang string, scope deckbox.Scope) string {
	keys := keysFor(scope)
	query := r.SearchQueries[0]

	if len(r.Aggregates) == 0 {
		return fmt.Sprintf(i18n.T(lang, keys.noResults), query)
	}

	var response strings.Builder
	fmt.Fprintf(&response, i18n.T(lang, keys.resultsHeader), query)

	// The encoded query is the same for every result; encode once.
	uEnc := b64.URLEncoding.EncodeToString([]byte(query))
	for _, agg := range r.Aggregates {
		linkURL := fmt.Sprintf("%s/sets/%d?f=17%v", deckbox.DeckboxBaseURL, agg.ListId, uEnc)
		fmt.Fprintf(&response, i18n.T(lang, "search.deckbox_link"), linkURL, agg.DeckboxLogin)
		writeOwnerMention(&response, lang, agg)
		response.WriteString(":\n")

		for _, cardName := range sortedCardNames(agg.FoundCards) {
			fmt.Fprintf(&response, "%s: %d\n", cardName, agg.FoundCards[cardName])
		}
	}

	return response.String()
}

// renderMulti groups by owner and ranks them by how much of the requested set
// they hold, so the best trade partner comes first.
func renderMulti(r deckbox.SearchResult, lang string, scope deckbox.Scope) (string, string) {
	keys := keysFor(scope)
	totalQueried := len(r.SearchQueries)

	var notFoundMsg string
	if len(r.NotFound) > 0 {
		notFoundMsg = fmt.Sprintf(i18n.T(lang, keys.multiNotFound), strings.Join(r.NotFound, ", "))
	}

	if len(r.Aggregates) == 0 {
		return fmt.Sprintf(i18n.T(lang, keys.multiNoResults), totalQueried), ""
	}

	var response strings.Builder
	fmt.Fprintf(&response, i18n.T(lang, keys.multiHeader), totalQueried)

	for _, agg := range r.Aggregates {
		linkURL := fmt.Sprintf("%s/sets/%d", deckbox.DeckboxBaseURL, agg.ListId)
		fmt.Fprintf(&response, i18n.T(lang, "search.deckbox_link"), linkURL, agg.DeckboxLogin)
		writeOwnerMention(&response, lang, agg)
		fmt.Fprintf(&response, i18n.T(lang, "search.multi_user_header"), agg.UniqueCount, totalQueried)

		for _, cardName := range sortedCardNames(agg.FoundCards) {
			fmt.Fprintf(&response, "  %s: %d\n", cardName, agg.FoundCards[cardName])
		}
		response.WriteString("\n")
	}

	return strings.TrimRight(response.String(), "\n"), notFoundMsg
}

// writeOwnerMention appends a Telegram mention when the list's owner is also a
// registered Bot User.
func writeOwnerMention(response *strings.Builder, lang string, agg deckbox.UserSearchAggregate) {
	if agg.TelegramID != nil && agg.TelegramUsername != nil {
		fmt.Fprintf(response, i18n.T(lang, "search.telegram_owner"), *agg.TelegramID, *agg.TelegramUsername)
	}
}

func sortedCardNames(cards map[string]int16) []string {
	names := make([]string, 0, len(cards))
	for name := range cards {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

