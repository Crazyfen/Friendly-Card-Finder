// Package chat is the bot's conversational behaviour, shared by every Platform:
// the commands in command.go decide what to say, and the renderers here decide
// what a search result is made of. Both produce message.Message spans and leave
// the markup to each Platform's adapter, so nothing here knows a chat SDK.
package chat

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/message"
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

// RenderSearch returns (main, notFound). notFound is nil when every queried
// card was found at least once, and for a single-card search, where the main
// message already says so.
func RenderSearch(r deckbox.SearchResult, lang string, scope deckbox.Scope) (main, notFound message.Message) {
	if len(r.SearchQueries) == 1 {
		return renderSingle(r, lang, scope), nil
	}
	return renderMulti(r, lang, scope)
}

// renderSingle lists every matching card per owner, with a Deckbox link
// pre-filtered to the query.
func renderSingle(r deckbox.SearchResult, lang string, scope deckbox.Scope) message.Message {
	keys := keysFor(scope)
	query := r.SearchQueries[0]

	if len(r.Aggregates) == 0 {
		return i18n.Format(lang, keys.noResults, message.Bold(query))
	}

	m := i18n.Format(lang, keys.resultsHeader, message.Bold(query))

	// The encoded query is the same for every result; encode once.
	uEnc := b64.URLEncoding.EncodeToString([]byte(query))
	for _, agg := range r.Aggregates {
		linkURL := fmt.Sprintf("%s/sets/%d?f=17%v", deckbox.DeckboxBaseURL, agg.ListId, uEnc)
		m = appendOwner(m, lang, agg, linkURL)
		m = append(m, message.Text(":\n"))

		for _, cardName := range sortedCardNames(agg.FoundCards) {
			m = append(m, message.Text(fmt.Sprintf("%s: %d\n", cardName, agg.FoundCards[cardName])))
		}
	}

	return m
}

// renderMulti groups by owner and ranks them by how much of the requested set
// they hold, so the best trade partner comes first.
func renderMulti(r deckbox.SearchResult, lang string, scope deckbox.Scope) (message.Message, message.Message) {
	keys := keysFor(scope)
	totalQueried := count(len(r.SearchQueries))

	var notFound message.Message
	if len(r.NotFound) > 0 {
		notFound = i18n.Format(lang, keys.multiNotFound, message.Text(strings.Join(r.NotFound, ", ")))
	}

	if len(r.Aggregates) == 0 {
		return i18n.Format(lang, keys.multiNoResults, totalQueried), nil
	}

	m := i18n.Format(lang, keys.multiHeader, totalQueried)

	for _, agg := range r.Aggregates {
		linkURL := fmt.Sprintf("%s/sets/%d", deckbox.DeckboxBaseURL, agg.ListId)
		m = appendOwner(m, lang, agg, linkURL)
		m = append(m, i18n.Format(lang, "search.multi_user_header", count(agg.UniqueCount), totalQueried)...)

		for _, cardName := range sortedCardNames(agg.FoundCards) {
			m = append(m, message.Text(fmt.Sprintf("  %s: %d\n", cardName, agg.FoundCards[cardName])))
		}
		m = append(m, message.Text("\n"))
	}

	return trimTrailingNewlines(m), notFound
}

// appendOwner appends the Deckbox link for the list and, when the owner can be
// reached by anyone, their Contacts. Which Contacts show, and how, is up to the
// reader's Platform.
func appendOwner(m message.Message, lang string, agg deckbox.UserSearchAggregate, linkURL string) message.Message {
	m = append(m, i18n.Format(lang, "search.deckbox_link", message.Link(agg.DeckboxLogin, linkURL))...)
	if reachable(agg.Contacts) {
		m = append(m, i18n.Format(lang, "search.owner", message.Contacts(agg.Contacts))...)
	}
	return m
}

// reachable reports whether any Contact has a Handle. Every formatter drops a
// Contact without one, so without this check the owner clause could render as
// a bare " from ".
func reachable(cs []deckbox.Contact) bool {
	for _, c := range cs {
		if c.Handle != nil {
			return true
		}
	}
	return false
}

// trimTrailingNewlines drops the newlines the last owner block ends with.
func trimTrailingNewlines(m message.Message) message.Message {
	for len(m) > 0 && m[len(m)-1].Kind == message.KindText {
		last := &m[len(m)-1]
		last.Text = strings.TrimRight(last.Text, "\n")
		if last.Text != "" {
			break
		}
		m = m[:len(m)-1]
	}
	return m
}

func sortedCardNames(cards map[string]int16) []string {
	names := make([]string, 0, len(cards))
	for name := range cards {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
