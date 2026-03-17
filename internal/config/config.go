package config

import (
	"FriendlyCardFinder/env"
	"log"
	"strconv"
)

type Config struct {
	StoragePath             string
	BotToken                string
	Env                     string
	DeckboxSessionCookie    string
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

	return &Config{
		StoragePath:             env.StoragePath.GetValue(),
		BotToken:                env.BotToken.GetValue(),
		Env:                     env.Env.GetValue(),
		DeckboxSessionCookie:    env.DeckboxSessionCookie.GetValue(),
		FreshnessTimeLimitHours: freshnessHours,
		CardListRefreshHours:    cardListRefreshHours,
		CardListBatchSize:       cardListBatchSize,
	}
}
