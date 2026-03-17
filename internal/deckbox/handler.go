package deckbox

import (
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

func NewUser(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper *Scraper, message *models.Message, lang string) string {
	const op = "handlers.deckbox.NewUser"
	log = log.With(slog.String("operation", op), slog.String("message_id", strconv.Itoa(message.ID)))

	argument := commandArguments(message)
	if argument == "" {
		log.Info("forgotten deckbox login")
		return i18n.T(lang, "deckbox.register_no_argument")
	}

	log.Info("registering user", slog.String("username", message.From.Username), slog.String("deckbox_id", argument))

	err := storage.RegisterUser(ctx, BotUser{
		TelegramID:       message.From.ID,
		TelegramUsername: message.From.Username,
		DeckboxLogin:     argument,
	})
	if err != nil {
		log.Error("failed to register user", sl.Err(err))
		return i18n.T(lang, "deckbox.register_error")
	}

	// Run profile refresh in the background with a timeout to avoid orphaned workers.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		GetProfileData(ctx, log, storage, scraper, argument)
	}()

	return fmt.Sprintf(i18n.T(lang, "deckbox.register_success"), message.From.FirstName, message.From.LastName)
}

func GetProfileData(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper *Scraper, deckboxLogin string) {
	const op = "handlers.deckbox.GetProfileData"
	log = log.With(slog.String("operation", op), slog.String("deckbox_id", deckboxLogin))

	select {
	case <-ctx.Done():
		log.Warn("profile data refresh canceled", slog.String("reason", ctx.Err().Error()))
		return
	default:
	}

	user, err := scraper.FetchDeckboxUserProfile(ctx, deckboxLogin)
	if err != nil {
		log.Error("failed to fetch user profile", sl.Err(err))
		return
	}

	// Process userData and update storage
	err = storage.SaveDeckboxUser(ctx, user)
	if err != nil {
		log.Error("failed to update user", sl.Err(err))
		return
	}

	// Update card lists (performed synchronously to avoid unbounded goroutine spawning)
	if user.InventoryID != nil {
		UpdateCardList(ctx, log, storage, scraper, *user.InventoryID)
	}
	if user.TradelistID != nil {
		UpdateCardList(ctx, log, storage, scraper, *user.TradelistID)
	}
	if user.WishlistID != nil {
		UpdateCardList(ctx, log, storage, scraper, *user.WishlistID)
	}
	log.Info("successfully updated profile data", slog.String("deckbox_id", deckboxLogin))
}

func UpdateCardList(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper *Scraper, listId int64) {
	const op = "handlers.deckbox.UpdateCardList"
	log = log.With(slog.String("operation", op), slog.Int64("list_id", listId))

	select {
	case <-ctx.Done():
		log.Warn("card list update canceled", slog.String("reason", ctx.Err().Error()))
		return
	default:
	}

	// Fetch the latest card list from Deckbox
	cardList, err := scraper.FetchCardList(ctx, listId)
	if err != nil {
		log.Error("failed to fetch card list", sl.Err(err))
		return
	}

	// Save the card list to storage
	err = storage.SaveCardList(ctx, cardList)
	if err != nil {
		log.Error("failed to save card list", sl.Err(err))
		return
	}

	log.Info("successfully updated card list", slog.Int64("list_id", listId))
}

type profileFetcher interface {
	FetchDeckboxUserProfile(ctx context.Context, login string) (DeckboxUser, error)
	FetchCardList(ctx context.Context, listId int64) (CardList, error)
}

func refreshUserListsWorker(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper profileFetcher, login string) (cardCount int, errStr string) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err().Error()
	default:
	}

	user, err := scraper.FetchDeckboxUserProfile(ctx, login)
	if err != nil {
		log.Error("failed to fetch user profile", sl.Err(err))
		return 0, fmt.Sprintf("failed to fetch profile: %v", err)
	}

	err = storage.SaveDeckboxUser(ctx, user)
	if err != nil {
		log.Error("failed to save deckbox user", sl.Err(err))
		return 0, fmt.Sprintf("failed to save user: %v", err)
	}

	totalCards := 0

	if user.InventoryID != nil {
		cardList, err := scraper.FetchCardList(ctx, *user.InventoryID)
		if err != nil {
			log.Error("failed to fetch inventory", sl.Err(err))
		} else if err = storage.SaveCardList(ctx, cardList); err != nil {
			log.Error("failed to save inventory", sl.Err(err))
		} else {
			totalCards += len(cardList.Cards)
		}
	}

	if user.TradelistID != nil {
		cardList, err := scraper.FetchCardList(ctx, *user.TradelistID)
		if err != nil {
			log.Error("failed to fetch tradelist", sl.Err(err))
		} else if err = storage.SaveCardList(ctx, cardList); err != nil {
			log.Error("failed to save tradelist", sl.Err(err))
		} else {
			totalCards += len(cardList.Cards)
		}
	}

	if user.WishlistID != nil {
		cardList, err := scraper.FetchCardList(ctx, *user.WishlistID)
		if err != nil {
			log.Error("failed to fetch wishlist", sl.Err(err))
		} else if err = storage.SaveCardList(ctx, cardList); err != nil {
			log.Error("failed to save wishlist", sl.Err(err))
		} else {
			totalCards += len(cardList.Cards)
		}
	}

	// Update timestamp only if card list saves succeeded
	if totalCards > 0 {
		err = storage.UpdateDeckboxUserTimestamp(ctx, login, time.Now().Unix())
		if err != nil {
			log.Error("failed to update timestamp", sl.Err(err))
			return 0, fmt.Sprintf("failed to update timestamp: %v", err)
		}
	} else {
		// No cards processed indicates possible errors fetching/saving lists
		return 0, "failed to refresh lists or no cards found"
	}

	return totalCards, ""
}

func runUserRefreshWorkerPool(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper profileFetcher, logins []string, numWorkers int) []struct {
	login string
	cards int
	err   string
} {
	jobs := make(chan string, len(logins))
	results := make(chan struct {
		login string
		cards int
		err   string
	}, len(logins))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case login, ok := <-jobs:
					if !ok {
						return
					}
					workerLog := log.With(slog.String("deckbox_id", login), slog.Int("worker_id", workerID))
					cards, errStr := refreshUserListsWorker(ctx, workerLog, storage, scraper, login)
					results <- struct {
						login string
						cards int
						err   string
					}{login: login, cards: cards, err: errStr}
				}
			}
		}(i)
	}

	// Send jobs
	go func() {
		for _, login := range logins {
			jobs <- strings.TrimSpace(login)
		}
		close(jobs)
	}()

	// Collect results
	collectedResults := []struct {
		login string
		cards int
		err   string
	}{}
	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		collectedResults = append(collectedResults, res)
	}

	return collectedResults
}

func RefreshStaleUserLists(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper profileFetcher, refreshHours int) {
	const op = "handlers.deckbox.RefreshStaleUserLists"
	log = log.With(slog.String("operation", op))

	thresholdTime := time.Now().Add(-time.Duration(refreshHours) * time.Hour).Unix()
	staleLogins, err := storage.GetAllDeckboxUsersWithOldLists(ctx, thresholdTime)
	if err != nil {
		log.Error("failed to get stale users", sl.Err(err))
		return
	}

	if len(staleLogins) == 0 {
		log.Debug("no stale users to refresh")
		return
	}

	log.Info("starting refresh of stale user lists", slog.Int("user_count", len(staleLogins)))

	collectedResults := runUserRefreshWorkerPool(ctx, log, storage, scraper, staleLogins, 10)
	for _, res := range collectedResults {
		if res.err != "" {
			log.Error("failed to refresh user", slog.String("deckbox_id", res.login), slog.String("error", res.err))
		}
	}

	log.Info("completed refresh of stale user lists")
}

func SearchCard(ctx context.Context, log *slog.Logger, storage DeckboxSaver, cardName string, scope string) (SearchCardResult, error) {
	const op = "handlers.deckbox.SearchCard"
	log = log.With(slog.String("operation", op), slog.String("card_name", cardName), slog.String("scope", scope))

	results, err := storage.SearchCard(ctx, cardName, scope)
	if err != nil {
		log.Error("failed to search card", sl.Err(err))
		return SearchCardResult{}, err
	}

	var currentListId int64 = -1
	var cardList CardList
	var searchResults []CardList

	for _, result := range results {
		if currentListId != result.ListId {
			if currentListId != -1 {
				searchResults = append(searchResults, cardList)
			}
			currentListId = result.ListId
			cardList = CardList{
				ListId: currentListId,
				Cards:  make(map[string]int16),
			}
		}
		cardList.AddCard(result.CardName, result.Quantity)
	}

	if len(cardList.Cards) > 0 {
		searchResults = append(searchResults, cardList)
	}

	return SearchCardResult{SearchQuery: cardName, SearchResults: searchResults}, nil
}

func commandArguments(m *models.Message) string {
	if len(m.Entities) == 0 {
		return ""
	}
	entity := m.Entities[0]

	if entity.Type != models.MessageEntityTypeBotCommand {
		return ""
	}

	if len(m.Text) == entity.Length {
		return ""
	}

	return m.Text[entity.Length+1:]
}

type SuggestDeckboxResult struct {
	ProcessedCount int
	SkippedCount   int
	TotalCards     int
	Errors         []string
}

func SuggestDeckbox(ctx context.Context, log *slog.Logger, storage DeckboxSaver, scraper profileFetcher, logins []string, freshnessHours int) SuggestDeckboxResult {
	const op = "handlers.deckbox.SuggestDeckbox"
	log = log.With(slog.String("operation", op))

	result := SuggestDeckboxResult{
		Errors: []string{},
	}

	if len(logins) == 0 {
		return result
	}

	log.Info("starting suggest deckbox refresh", slog.Int("user_count", len(logins)))

	// Pre-filter: freshness checks are cheap (one DB read each) and serial access
	// avoids races on result.Errors / result.SkippedCount inside worker goroutines.
	freshnessLimit := time.Duration(freshnessHours) * time.Hour
	toProcess := make([]string, 0, len(logins))
	for _, login := range logins {
		login = strings.TrimSpace(login)
		existingUser, err := storage.GetDeckboxUser(ctx, login)
		if err != nil {
			log.Error("failed to get existing user", sl.Err(err))
			result.Errors = append(result.Errors, fmt.Sprintf("failed to check existing user: %v", err))
			continue
		}
		if existingUser != nil && existingUser.UpdatedAt != nil {
			timeSinceLast := time.Since(time.Unix(*existingUser.UpdatedAt, 0))
			if timeSinceLast < freshnessLimit {
				log.With(slog.String("deckbox_id", login)).Info("skipping, data too fresh", slog.Duration("time_since_update", timeSinceLast), slog.Duration("freshness_limit", freshnessLimit))
				result.SkippedCount++
				continue
			}
		}
		toProcess = append(toProcess, login)
	}

	if len(toProcess) == 0 {
		return result
	}

	collectedResults := runUserRefreshWorkerPool(ctx, log, storage, scraper, toProcess, 5)

	for _, res := range collectedResults {
		if res.err != "" {
			result.Errors = append(result.Errors, res.err)
		} else if res.cards > 0 {
			result.ProcessedCount++
			result.TotalCards += res.cards
		}
	}

	return result
}
