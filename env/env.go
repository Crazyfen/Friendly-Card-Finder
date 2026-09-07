package env

import (
	"os"

	"github.com/joho/godotenv"
)

type EnvKey string

func (key EnvKey) GetValue() string {
	return os.Getenv(string(key))
}

const (
	BotToken                EnvKey = "BOT_TOKEN"
	StoragePath             EnvKey = "STORAGE_PATH"
	Env                     EnvKey = "ENV"
	DeckboxSessionCookie    EnvKey = "DECKBOX_SESSION_COOKIE"
	DeckboxLogin            EnvKey = "DECKBOX_LOGIN"
	DeckboxPassword         EnvKey = "DECKBOX_PASSWORD"
	FreshnessTimeLimitHours EnvKey = "FRESHNESS_TIME_LIMIT_HOURS"
	CardListRefreshHours    EnvKey = "CARD_LIST_REFRESH_HOURS"
	CardListBatchSize       EnvKey = "CARD_LIST_BATCH_SIZE"
	AdminAddr               EnvKey = "ADMIN_ADDR"
	AdminUser               EnvKey = "ADMIN_USER"
	AdminPassword           EnvKey = "ADMIN_PASSWORD"
)

func Load() error {
	return godotenv.Load(".env")
}
