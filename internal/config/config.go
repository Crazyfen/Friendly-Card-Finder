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
}

// envInt reads key as an int, falling back to def when unset or unparsable.
func envInt(key env.EnvKey, def int) int {
	if s := key.GetValue(); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil {
			return parsed
		}
	}
	return def
}

func MustLoad() *Config {
	err := env.Load()
	if err != nil {
		log.Fatal(err)
	}

	freshnessHours := envInt(env.FreshnessTimeLimitHours, 24)
	cardListRefreshHours := envInt(env.CardListRefreshHours, 3)

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
	}
}
