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
	FreshnessTimeLimitHours EnvKey = "FRESHNESS_TIME_LIMIT_HOURS"
	CardListRefreshHours    EnvKey = "CARD_LIST_REFRESH_HOURS"
	CardListBatchSize       EnvKey = "CARD_LIST_BATCH_SIZE"
)

func Load() error {
	return godotenv.Load(".env")
}
