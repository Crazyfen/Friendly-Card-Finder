package telegram_test

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/telegram"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestNewRequestStripsTheCommand(t *testing.T) {
	tests := []struct {
		name    string
		message *models.Message
		want    string
	}{
		{
			name:    "plain message keeps its text",
			message: &models.Message{Text: "Lightning Bolt"},
			want:    "Lightning Bolt",
		},
		{
			name: "command argument survives the prefix",
			message: &models.Message{
				Text:     "/deckbox petya_db",
				Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Length: 8}},
			},
			want: "petya_db",
		},
		{
			name: "command with no argument is empty, not the command",
			message: &models.Message{
				Text:     "/sell",
				Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Length: 5}},
			},
			want: "",
		},
		{
			name: "a leading non-command entity is not stripped",
			message: &models.Message{
				Text:     "Sméagol, Helpful Guide",
				Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBold, Length: 7}},
			},
			want: "Sméagol, Helpful Guide",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := telegram.NewRequest(tt.message).Text; got != tt.want {
				t.Errorf("Text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRequestCarriesTheSender(t *testing.T) {
	got := telegram.NewRequest(&models.Message{
		Text: "Shock",
		From: &models.User{ID: 7, Username: "petya", FirstName: "Petya", LastName: "P", LanguageCode: "ru-RU"},
	})

	if got.Platform != deckbox.PlatformTelegram {
		t.Errorf("Platform = %q, want %q", got.Platform, deckbox.PlatformTelegram)
	}
	if got.UserID != 7 || got.Username != "petya" || got.FirstName != "Petya" || got.LastName != "P" || got.Lang != "ru" {
		t.Errorf("sender not carried through: %+v", got)
	}
}
