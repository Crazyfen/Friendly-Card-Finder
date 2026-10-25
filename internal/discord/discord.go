package discord

import (
	"FriendlyCardFinder/internal/chat"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/i18n"
	"FriendlyCardFinder/internal/lib/logger/sl"
	"FriendlyCardFinder/internal/lib/tgutil"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// messageLimit is the largest message Discord accepts, in bytes.
const messageLimit = 2000

// modalInput is the CustomID of the one text box every modal holds.
const modalInput = "text"

// Adapter serves chat.Commands over the Discord gateway: slash commands, the
// modal that takes a multi-line paste, DMs and messages that mention the bot.
// Like the Telegram adapter it decides nothing a user can see.
type Adapter struct {
	log *slog.Logger
	cmd *chat.Commands
	s   *discordgo.Session
	// ctx is the one passed to Run. discordgo handlers receive no context, so
	// commands and the Suggest goroutine run under this one.
	ctx context.Context
}

func New(log *slog.Logger, cmd *chat.Commands, token string) (*Adapter, error) {
	const op = "discord.New"

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	// No Message Content intent: Discord still delivers the text of DMs and of
	// messages that mention the bot, which is all the bot reads (ADR 0012).
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages

	a := &Adapter{log: log, cmd: cmd, s: s, ctx: context.Background()}
	s.AddHandler(a.onReady)
	s.AddHandler(a.onInteraction)
	s.AddHandler(a.onMessage)
	return a, nil
}

// Run connects to Discord and serves events until ctx ends.
func (a *Adapter) Run(ctx context.Context) error {
	const op = "discord.Run"
	log := a.log.With(slog.String("operation", op))

	// Set before Open: no handler runs until the gateway is connected.
	a.ctx = ctx
	if err := a.s.Open(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	log.Info("starting bot")

	<-ctx.Done()
	if err := a.s.Close(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func applicationCommands() []*discordgo.ApplicationCommand {
	option := func(name, description string, required bool) []*discordgo.ApplicationCommandOption {
		return []*discordgo.ApplicationCommandOption{{
			Type: discordgo.ApplicationCommandOptionString, Name: name, Description: description, Required: required,
		}}
	}
	cmds := []*discordgo.ApplicationCommand{
		{Name: "search", Description: "Search tradelists for a card; leave empty to paste several",
			Options: option("card", "One card name, taken verbatim", false)},
		{Name: "sell", Description: "Search wishlists for a card; leave empty to paste several",
			Options: option("card", "One card name, taken verbatim", false)},
		{Name: "deckbox", Description: "Register your Deckbox login",
			Options: option("login", "Your Deckbox login", true)},
		{Name: "suggestdeckbox", Description: "Refresh the lists of several Deckbox logins"},
		{Name: "help", Description: "How to use the bot"},
	}

	contexts := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM}
	installs := &[]discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}
	for _, c := range cmds {
		c.Contexts = contexts
		c.IntegrationTypes = installs
	}
	return cmds
}

// interactionRequest reduces an interaction to a Request. A guild interaction
// carries its user in Member, a DM one in User.
func interactionRequest(i *discordgo.Interaction, text string) chat.Request {
	u := i.User
	if i.Member != nil && i.Member.User != nil {
		u = i.Member.User
	}
	return userRequest(u, text, i18n.DetectLang(string(i.Locale)))
}

// messageRequest reduces a DM, or a guild message that mentions the bot, to a
// Request. Anything else — another guild message, a bot, a bare mention — is
// not for the bot. A message carries no locale, so it is answered in English.
func messageRequest(m *discordgo.Message, botID string) (chat.Request, bool) {
	if m.Author == nil || m.Author.Bot {
		return chat.Request{}, false
	}

	text := m.Content
	if m.GuildID != "" {
		stripped := strings.NewReplacer("<@"+botID+">", "", "<@!"+botID+">", "").Replace(text)
		if stripped == text {
			return chat.Request{}, false
		}
		text = strings.TrimSpace(stripped)
	}
	if strings.TrimSpace(text) == "" {
		return chat.Request{}, false
	}
	return userRequest(m.Author, text, "en"), true
}

func userRequest(u *discordgo.User, text, lang string) chat.Request {
	req := chat.Request{Text: text, Lang: lang, Platform: deckbox.PlatformDiscord}
	if u == nil {
		return req
	}
	req.UserID, _ = strconv.ParseInt(u.ID, 10, 64)
	req.Username = u.Username
	req.FirstName = u.DisplayName() // GlobalName, falling back to Username
	return req
}

// parts formats a Message and splits it at Discord's limit. An empty Message
// gives no parts rather than one empty one, which Discord would reject.
func parts(r chat.Reply) []string {
	text := Format(r.Message)
	if text == "" {
		return nil
	}
	return tgutil.SplitMessage(text, messageLimit)
}

// messageSend turns a reply into channel messages, one per part. They ping
// nobody: an empty AllowedMentions stops @everyone, roles and every user,
// including the owners listed in a result and the author being replied to.
func messageSend(r chat.Reply) []*discordgo.MessageSend {
	var sends []*discordgo.MessageSend
	for _, part := range parts(r) {
		sends = append(sends, &discordgo.MessageSend{
			Content:         part,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
			Flags:           discordgo.MessageFlagsSuppressEmbeds,
		})
	}
	return sends
}

// webhookParams turns replies into interaction messages, one per part, each
// ephemeral when its reply is Private. Like messageSend they ping nobody.
func webhookParams(replies []chat.Reply) []*discordgo.WebhookParams {
	var params []*discordgo.WebhookParams
	for _, r := range replies {
		flags := discordgo.MessageFlagsSuppressEmbeds
		if r.Private {
			flags |= discordgo.MessageFlagsEphemeral
		}
		for _, part := range parts(r) {
			params = append(params, &discordgo.WebhookParams{
				Content:         part,
				AllowedMentions: &discordgo.MessageAllowedMentions{},
				Flags:           flags,
			})
		}
	}
	return params
}

// sender is the slice of *discordgo.Session that replying needs, so tests fake it.
type sender interface {
	InteractionRespond(i *discordgo.Interaction, resp *discordgo.InteractionResponse, opts ...discordgo.RequestOption) error
	FollowupMessageCreate(i *discordgo.Interaction, wait bool, data *discordgo.WebhookParams, opts ...discordgo.RequestOption) (*discordgo.Message, error)
	InteractionResponseDelete(i *discordgo.Interaction, opts ...discordgo.RequestOption) error
}

// followUp answers a deferred interaction. With nothing to say it deletes the
// "thinking…" placeholder, which would otherwise stay up for good.
func followUp(s sender, i *discordgo.Interaction, replies []chat.Reply) error {
	params := webhookParams(replies)
	if len(params) == 0 {
		return s.InteractionResponseDelete(i)
	}
	return sendFollowups(s, i, params)
}

func sendFollowups(s sender, i *discordgo.Interaction, params []*discordgo.WebhookParams) error {
	for _, p := range params {
		if _, err := s.FollowupMessageCreate(i, true, p); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) onReady(s *discordgo.Session, r *discordgo.Ready) {
	const op = "discord.onReady"
	log := a.log.With(slog.String("operation", op))

	// Global commands; overwriting on every Ready keeps them in step with the code.
	if _, err := s.ApplicationCommandBulkOverwrite(r.Application.ID, "", applicationCommands()); err != nil {
		log.Error("failed to register commands", sl.Err(err))
		return
	}
	log.Info("registered commands", slog.String("user", r.User.Username))
}

func (a *Adapter) onInteraction(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	const op = "discord.onInteraction"
	i := ic.Interaction
	log := a.log.With(slog.String("operation", op), slog.String("interaction_id", i.ID))

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		a.command(log, a.s, i)

	case discordgo.InteractionModalSubmit:
		d := i.ModalSubmitData()
		log = log.With(slog.String("modal", d.CustomID))
		log.Info("handling modal")

		text := modalText(d)
		switch d.CustomID {
		case "search", "sell":
			a.search(log, a.s, i, d.CustomID, text)
		case "suggestdeckbox":
			a.suggest(log, i, text)
		}
	}
}

// command answers a slash command.
func (a *Adapter) command(log *slog.Logger, s sender, i *discordgo.Interaction) {
	d := i.ApplicationCommandData()
	log = log.With(slog.String("command", d.Name))
	log.Info("handling command")

	text := ""
	if len(d.Options) > 0 {
		text = d.Options[0].StringValue()
	}
	switch d.Name {
	case "search", "sell":
		if strings.TrimSpace(text) == "" {
			respond(log, s, i, modal(d.Name, i18n.DetectLang(string(i.Locale))))
			return
		}
		a.search(log, s, i, d.Name, text)
	case "suggestdeckbox":
		respond(log, s, i, modal(d.Name, i18n.DetectLang(string(i.Locale))))
	case "deckbox":
		deferred(log, s, i, true, func() []chat.Reply { return a.cmd.Register(a.ctx, interactionRequest(i, text)) })
	case "help":
		answer(log, s, i, a.cmd.Start(a.ctx, interactionRequest(i, text)))
	}
}

// modal is the form a command opens when its input may span several lines:
// slash-command options cannot hold newlines (ADR 0012).
func modal(name, lang string) *discordgo.InteractionResponse {
	title, label := "discord.modal."+name, "discord.modal.label.cards"
	if name == "suggestdeckbox" {
		title, label = "discord.modal.suggest", "discord.modal.label.logins"
	}
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: name,
			Title:    i18n.T(lang, title),
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{discordgo.TextInput{
					CustomID:  modalInput,
					Label:     i18n.T(lang, label),
					Style:     discordgo.TextInputParagraph,
					Required:  true,
					MaxLength: 4000,
				}},
			}},
		},
	}
}

// modalText is the value of the modal's text box.
func modalText(d discordgo.ModalSubmitInteractionData) string {
	for _, c := range d.Components {
		row, ok := c.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, rc := range row.Components {
			if in, ok := rc.(*discordgo.TextInput); ok && in.CustomID == modalInput {
				return in.Value
			}
		}
	}
	return ""
}

func respond(log *slog.Logger, s sender, i *discordgo.Interaction, resp *discordgo.InteractionResponse) bool {
	if err := s.InteractionRespond(i, resp); err != nil {
		log.Error("failed to respond", sl.Err(err))
		return false
	}
	return true
}

// search posts its result publicly after a deferral.
func (a *Adapter) search(log *slog.Logger, s sender, i *discordgo.Interaction, name, text string) {
	run := a.cmd.Search
	if name == "sell" {
		run = a.cmd.Sell
	}
	deferred(log, s, i, false, func() []chat.Reply { return run(a.ctx, interactionRequest(i, text)) })
}

// deferred acknowledges at once, then runs the Command and posts its replies as
// follow-ups. Any Command that reaches storage goes through here: a search can
// take a while, and a write queues behind a Refresh on the single write
// connection, either of which can outlast Discord's three-second window. The
// placeholder's visibility is the first follow-up's, so a Command whose replies
// are Private defers ephemerally.
func deferred(log *slog.Logger, s sender, i *discordgo.Interaction, private bool, run func() []chat.Reply) {
	resp := &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource}
	if private {
		resp.Data = &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}
	}
	if !respond(log, s, i, resp) {
		return
	}
	if err := followUp(s, i, run()); err != nil {
		log.Error("failed to send response", sl.Err(err))
	}
}

// answer responds with the first part of replies at once and follows up with
// the rest. Only for Commands that never touch storage.
func answer(log *slog.Logger, s sender, i *discordgo.Interaction, replies []chat.Reply) {
	params := webhookParams(replies)
	if len(params) == 0 {
		return
	}
	first := params[0]
	if !respond(log, s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: first.Content, AllowedMentions: first.AllowedMentions, Flags: first.Flags},
	}) {
		return
	}
	if err := sendFollowups(s, i, params[1:]); err != nil {
		log.Error("failed to send response", sl.Err(err))
	}
}

// suggest acknowledges now and posts the summary when the bulk Refresh ends.
// The summary is a channel message, not a follow-up: it can arrive after the
// interaction token's 15 minutes are up, so it pings the requester instead
// (ADR 0012) — the one message that pings anyone.
func (a *Adapter) suggest(log *slog.Logger, i *discordgo.Interaction, text string) {
	req := interactionRequest(i, text)
	ack, run := a.cmd.Suggest(req)
	answer(log, a.s, i, []chat.Reply{ack})
	if run == nil {
		return
	}

	userID := strconv.FormatInt(req.UserID, 10)
	go func() {
		summary := run(a.ctx)
		text := Format(summary.Message)
		if text == "" {
			return
		}
		for _, part := range tgutil.SplitMessage("<@"+userID+"> "+text, messageLimit) {
			if _, err := a.s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
				Content:         part,
				AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{userID}},
				Flags:           discordgo.MessageFlagsSuppressEmbeds,
			}); err != nil {
				log.Error("failed to send summary", sl.Err(err))
				return
			}
		}
	}()
}

func (a *Adapter) onMessage(s *discordgo.Session, mc *discordgo.MessageCreate) {
	const op = "discord.onMessage"

	req, ok := messageRequest(mc.Message, s.State.User.ID)
	if !ok {
		return
	}
	log := a.log.With(slog.String("operation", op), slog.String("message_id", mc.ID))
	log.Info("handling message")

	for _, r := range a.cmd.Search(a.ctx, req) {
		for _, ms := range messageSend(r) {
			if r.ReplyTo {
				ms.Reference = mc.Reference()
			}
			if _, err := s.ChannelMessageSendComplex(mc.ChannelID, ms); err != nil {
				log.Error("failed to send response", sl.Err(err))
				return
			}
		}
	}
}
