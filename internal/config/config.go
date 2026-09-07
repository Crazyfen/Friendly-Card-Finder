package config

import (
	"FriendlyCardFinder/env"
	"cmp"
	"errors"
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
	AdminAddr               string
	AdminUser               string
	AdminPassword           string
}

// defaultAdminAddr is where the Admin Panel listens inside the container. Caddy
// reaches it over the compose network; the port is never published to the host,
// so the proxy is the only route in. See docs/adr/0003.
const defaultAdminAddr = ":8081"

// envInt reads key as an int, falling back to def when unset or unparsable.
func envInt(key env.EnvKey, def int) int {
	if s := key.GetValue(); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil {
			return parsed
		}
	}
	return def
}

// MustLoad is Load with the only reaction main has to a bad configuration.
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		log.Fatal(err)
	}
	return cfg
}

// Load reads and validates the environment. It returns an error rather than
// exiting so the rules that decide whether the bot may start are testable; the
// process-ending version is MustLoad.
//
// A missing .env is not an error: the VPS supplies the environment directly.
func Load() (*Config, error) {
	_ = env.Load()

	storagePath := env.StoragePath.GetValue()
	if storagePath == "" {
		return nil, errors.New("STORAGE_PATH is not set")
	}
	if env.BotToken.GetValue() == "" {
		return nil, errors.New("BOT_TOKEN is not set")
	}

	deckboxLogin := env.DeckboxLogin.GetValue()
	deckboxPassword := env.DeckboxPassword.GetValue()
	deckboxSessionCookie := env.DeckboxSessionCookie.GetValue()

	// Scraper auth requires either credentials to log in, or a manual cookie override.
	if (deckboxLogin == "" || deckboxPassword == "") && deckboxSessionCookie == "" {
		return nil, errors.New("deckbox auth not configured: set DECKBOX_LOGIN + DECKBOX_PASSWORD, or DECKBOX_SESSION_COOKIE")
	}

	return &Config{
		AdminAddr:               cmp.Or(env.AdminAddr.GetValue(), defaultAdminAddr),
		AdminUser:               env.AdminUser.GetValue(), // default lives in admin.New
		AdminPassword:           env.AdminPassword.GetValue(),
		StoragePath:             storagePath,
		BotToken:                env.BotToken.GetValue(),
		Env:                     env.Env.GetValue(),
		DeckboxSessionCookie:    deckboxSessionCookie,
		DeckboxLogin:            deckboxLogin,
		DeckboxPassword:         deckboxPassword,
		DeckboxCookiePath:       filepath.Join(filepath.Dir(storagePath), "deckbox_session"),
		FreshnessTimeLimitHours: envInt(env.FreshnessTimeLimitHours, 24),
		CardListRefreshHours:    envInt(env.CardListRefreshHours, 3),
		CardListBatchSize:       envInt(env.CardListBatchSize, 0), // 0: sqlite.New applies its own default
	}, nil
}
