package deckbox

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly"
)

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

	c := colly.NewCollector(
		colly.AllowedDomains("deckbox.org", "www.deckbox.org"),
	)

	c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"

	c.OnRequest(func(r *colly.Request) {
		cookies := []*http.Cookie{
			{
				Name:   "_tcg_session",
				Value:  s.sessionCookie,
				Domain: r.URL.Host,
				Path:   "/",
			},
		}
		c.SetCookies(r.URL.String(), cookies)
	})

	cardList := CardList{
		ListId: listId,
		Cards:  make(map[string]int16),
	}

	c.OnHTML("body", func(e *colly.HTMLElement) {
		body, _ := e.DOM.Html()
		body = strings.ReplaceAll(body, "\n", "")
		elements := strings.SplitSeq(body, "<br/>")
		for element := range elements {
			element = strings.TrimSpace(element)
			if element == "" {
				continue
			}
			element = html.UnescapeString(element)
			parts := strings.SplitN(element, " ", 2)
			if len(parts) != 2 {
				log.Error("failed to parse element", slog.String("element", element))
				continue
			} else {
				quantity, err := strconv.Atoi(parts[0])
				if err != nil {
					log.Error("failed to parse quantity", slog.String("element", parts[0]), slog.String("error", err.Error()))
					continue
				}
				cardName := parts[1]
				cardList.AddCard(cardName, int16(quantity))
			}
		}
	})

	// Retry with a backoff for transient network errors
	var err error
	const maxAttempts = 3
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return CardList{}, ctx.Err()
		default:
		}

		err = c.Visit(ExportURL + fmt.Sprintf("%d", listId) + "/export")
		if err == nil {
			break
		}
		log.Warn("visit export failed, retrying", slog.Int("attempt", i+1), slog.String("error", err.Error()))

		select {
		case <-ctx.Done():
			return CardList{}, ctx.Err()
		case <-time.After(time.Duration(200*(1<<i)) * time.Millisecond):
		}
	}
	if err != nil {
		log.Error("failed to visit card list page", slog.String("deckbox_id", fmt.Sprintf("%d", listId)), slog.String("error", err.Error()))
		return CardList{}, err
	}

	return cardList, nil
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
