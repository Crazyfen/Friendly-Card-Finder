package config

import (
	"FriendlyCardFinder/env"
	"log"
	"path/filepath"
	"strconv"
)

type Config struct {
	StoragePath             string
	BotToken                string
	Env                     string
	DeckboxSessionCookie    string
	DeckboxLogin            string
	DeckboxPassword         string
	DeckboxCookiePath       string
	FreshnessTimeLimitHours int
	CardListRefreshHours    int
	CardListBatchSize       int
}

func MustLoad() *Config {
	err := env.Load()
	if err != nil {
		log.Fatal(err)
	}

	freshnessHours := 24
	if freshnessStr := env.FreshnessTimeLimitHours.GetValue(); freshnessStr != "" {
		if parsed, err := strconv.Atoi(freshnessStr); err == nil {
			freshnessHours = parsed
		}
	}

	cardListRefreshHours := 3
	if refreshStr := env.CardListRefreshHours.GetValue(); refreshStr != "" {
		if parsed, err := strconv.Atoi(refreshStr); err == nil {
			cardListRefreshHours = parsed
		}
	}

	cardListBatchSize := 1000
	if batchStr := env.CardListBatchSize.GetValue(); batchStr != "" {
		if parsed, err := strconv.Atoi(batchStr); err == nil && parsed > 0 {
			cardListBatchSize = parsed
		}
	}

	storagePath := env.StoragePath.GetValue()
	deckboxLogin := env.DeckboxLogin.GetValue()
	deckboxPassword := env.DeckboxPassword.GetValue()
	deckboxSessionCookie := env.DeckboxSessionCookie.GetValue()

	// Scraper auth requires either credentials to log in, or a manual cookie override.
	if (deckboxLogin == "" || deckboxPassword == "") && deckboxSessionCookie == "" {
		log.Fatal("deckbox auth not configured: set DECKBOX_LOGIN + DECKBOX_PASSWORD, or DECKBOX_SESSION_COOKIE")
	}

	return &Config{
		StoragePath:             storagePath,
		BotToken:                env.BotToken.GetValue(),
		Env:                     env.Env.GetValue(),
		DeckboxSessionCookie:    deckboxSessionCookie,
		DeckboxLogin:            deckboxLogin,
		DeckboxPassword:         deckboxPassword,
		DeckboxCookiePath:       filepath.Join(filepath.Dir(storagePath), "deckbox_session"),
		FreshnessTimeLimitHours: freshnessHours,
		CardListRefreshHours:    cardListRefreshHours,
		CardListBatchSize:       cardListBatchSize,
	}
}
