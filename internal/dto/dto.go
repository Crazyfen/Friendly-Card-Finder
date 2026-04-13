package dto

type CardSearchDTO struct {
	ListId           int64
	CardName         string
	Quantity         int16
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
}
