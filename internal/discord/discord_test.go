package discord

import (
	"FriendlyCardFinder/internal/chat"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/message"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestApplicationCommands(t *testing.T) {
	cmds := applicationCommands()

	var names []string
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	if want := []string{"search", "sell", "deckbox", "suggestdeckbox", "help"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}

	wantOption := map[string]struct {
		name     string
		required bool
	}{
		"search":  {"card", false},
		"sell":    {"card", false},
		"deckbox": {"login", true},
	}
	for _, c := range cmds {
		if c.Description == "" {
			t.Errorf("%s has no description", c.Name)
		}
		if c.Contexts == nil || !slices.Contains(*c.Contexts, discordgo.InteractionContextGuild) ||
			!slices.Contains(*c.Contexts, discordgo.InteractionContextBotDM) {
			t.Errorf("%s contexts = %v, want guild and bot DM", c.Name, c.Contexts)
		}

		want, hasOption := wantOption[c.Name]
		if !hasOption {
			if len(c.Options) != 0 {
				t.Errorf("%s options = %d, want none", c.Name, len(c.Options))
			}
			continue
		}
		if len(c.Options) != 1 {
			t.Fatalf("%s options = %d, want 1", c.Name, len(c.Options))
		}
		o := c.Options[0]
		if o.Name != want.name || o.Required != want.required || o.Type != discordgo.ApplicationCommandOptionString {
			t.Errorf("%s option = %q required=%v type=%v, want %q required=%v string",
				c.Name, o.Name, o.Required, o.Type, want.name, want.required)
		}
	}
}

func TestInteractionRequest(t *testing.T) {
	tests := []struct {
		name        string
		interaction *discordgo.Interaction
		want        chat.Request
	}{
		{
			name: "guild interaction takes its user from Member",
			interaction: &discordgo.Interaction{
				GuildID: "1",
				Locale:  "ru",
				Member:  &discordgo.Member{User: &discordgo.User{ID: "123456789012345678", Username: "ivan", GlobalName: "Ivan"}},
			},
			want: chat.Request{Text: "Shock", Lang: "ru", Platform: deckbox.PlatformDiscord,
				UserID: 123456789012345678, Username: "ivan", FirstName: "Ivan"},
		},
		{
			name: "DM interaction takes its user from User, first name falls back to Username",
			interaction: &discordgo.Interaction{
				Locale: "en-US",
				User:   &discordgo.User{ID: "42", Username: "bob"},
			},
			want: chat.Request{Text: "Shock", Lang: "en", Platform: deckbox.PlatformDiscord,
				UserID: 42, Username: "bob", FirstName: "bob"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interactionRequest(tt.interaction, "Shock"); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMessageRequest(t *testing.T) {
	const botID = "999"
	author := &discordgo.User{ID: "42", Username: "bob", GlobalName: "Bob"}
	tests := []struct {
		name    string
		message *discordgo.Message
		want    string
		ok      bool
	}{
		{"DM passes its whole content", &discordgo.Message{Content: "Shock\nOpt", Author: author}, "Shock\nOpt", true},
		{"mention is stripped", &discordgo.Message{GuildID: "1", Content: "<@999> Shock", Author: author}, "Shock", true},
		{"nickname mention is stripped", &discordgo.Message{GuildID: "1", Content: "Shock <@!999>\n", Author: author}, "Shock", true},
		{"guild message without mention", &discordgo.Message{GuildID: "1", Content: "Shock", Author: author}, "", false},
		{"mention alone", &discordgo.Message{GuildID: "1", Content: " <@999> ", Author: author}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := messageRequest(tt.message, botID)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			want := chat.Request{Text: tt.want, Lang: "en", Platform: deckbox.PlatformDiscord,
				UserID: 42, Username: "bob", FirstName: "Bob"}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestMessageRequestIgnoresBots(t *testing.T) {
	for _, id := range []string{"999", "7"} {
		m := &discordgo.Message{Content: "Shock", Author: &discordgo.User{ID: id, Bot: true}}
		if _, ok := messageRequest(m, "999"); ok {
			t.Errorf("bot %s message accepted", id)
		}
	}
}

func TestSearchRepliesPingNobody(t *testing.T) {
	r := chat.Reply{Message: message.Message{message.Text("<@1> @everyone @here")}, ReplyTo: true}
	for _, s := range messageSend(r) {
		am := s.AllowedMentions
		if am == nil || len(am.Parse) != 0 || len(am.Users) != 0 || len(am.Roles) != 0 || am.RepliedUser {
			t.Errorf("AllowedMentions = %+v, want none", am)
		}
		if s.Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
			t.Errorf("Flags = %v, want SuppressEmbeds", s.Flags)
		}
	}
}

func TestMessageSendSplits(t *testing.T) {
	sends := messageSend(chat.Reply{Message: message.Message{message.Text(strings.Repeat("a", 4500))}})
	if len(sends) != 3 {
		t.Fatalf("sends = %d, want 3", len(sends))
	}
	for _, s := range sends {
		if len(s.Content) > 2000 {
			t.Errorf("part of %d bytes", len(s.Content))
		}
	}
}

type fakeSender struct {
	responses []*discordgo.InteractionResponse
	followups []*discordgo.WebhookParams
	deletes   int
}

func (f *fakeSender) InteractionRespond(_ *discordgo.Interaction, resp *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	f.responses = append(f.responses, resp)
	return nil
}

func (f *fakeSender) FollowupMessageCreate(_ *discordgo.Interaction, _ bool, data *discordgo.WebhookParams, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.followups = append(f.followups, data)
	return &discordgo.Message{}, nil
}

func (f *fakeSender) InteractionResponseDelete(_ *discordgo.Interaction, _ ...discordgo.RequestOption) error {
	f.deletes++
	return nil
}

func TestDeferredWithNoRepliesDeletesPlaceholder(t *testing.T) {
	f := &fakeSender{}
	if err := followUp(f, &discordgo.Interaction{}, nil); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 || len(f.followups) != 0 {
		t.Errorf("deletes = %d, followups = %d; want 1, 0", f.deletes, len(f.followups))
	}
}

func TestFollowUpSendsEachPart(t *testing.T) {
	f := &fakeSender{}
	replies := []chat.Reply{
		{Message: message.Message{message.Text(strings.Repeat("a", 2500))}},
		{Message: message.Message{message.Text("private")}, Private: true},
	}
	if err := followUp(f, &discordgo.Interaction{}, replies); err != nil {
		t.Fatal(err)
	}
	if len(f.followups) != 3 || f.deletes != 0 {
		t.Fatalf("followups = %d, deletes = %d; want 3, 0", len(f.followups), f.deletes)
	}
	if got := f.followups[0].Content + f.followups[1].Content; got != strings.Repeat("a", 2500) {
		t.Errorf("first reply reassembled to %d bytes", len(got))
	}
	if f.followups[2].Content != "private" || f.followups[2].Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Errorf("third follow-up = %+v, want ephemeral \"private\"", f.followups[2])
	}
	if f.followups[0].Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Error("public reply sent ephemeral")
	}
	for _, p := range f.followups {
		if p.AllowedMentions == nil || len(p.AllowedMentions.Parse) != 0 {
			t.Errorf("AllowedMentions = %+v, want none", p.AllowedMentions)
		}
	}
}

func TestModalText(t *testing.T) {
	// The modal's value arrives as JSON; this checks discordgo decodes it into
	// the component types modalText looks for.
	raw := `{"type":5,"data":{"custom_id":"search","components":[{"type":1,"components":[{"type":4,"custom_id":"text","value":"Shock\nOpt"}]}]}}`
	var i discordgo.Interaction
	if err := json.Unmarshal([]byte(raw), &i); err != nil {
		t.Fatal(err)
	}
	if got := modalText(i.ModalSubmitData()); got != "Shock\nOpt" {
		t.Errorf("modalText = %q", got)
	}
}

// fakeDomain lets a test observe what the adapter had already sent when a
// Command reached the domain.
type fakeDomain struct{ onRegister func() }

func (d *fakeDomain) Search(context.Context, []string, deckbox.Scope) (deckbox.SearchResult, error) {
	return deckbox.SearchResult{}, nil
}

func (d *fakeDomain) Register(context.Context, deckbox.Registration) error {
	d.onRegister()
	return nil
}

func (d *fakeDomain) Suggest(context.Context, []string) deckbox.SuggestResult {
	return deckbox.SuggestResult{}
}

// Registering writes on the single write connection, which can queue behind a
// Refresh for longer than Discord's three-second window, so /deckbox must be
// acknowledged before the domain is called — privately, as its replies are.
func TestRegisterDefersBeforeStorage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &fakeSender{}
	respondedFirst := false
	dom := &fakeDomain{onRegister: func() { respondedFirst = len(f.responses) == 1 }}
	a := &Adapter{log: log, cmd: chat.NewCommands(log, dom), ctx: context.Background()}

	a.command(log, f, &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		User: &discordgo.User{ID: "9", Username: "alice_mtg"},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "deckbox",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "login", Type: discordgo.ApplicationCommandOptionString, Value: "alice"},
			},
		},
	})

	if !respondedFirst {
		t.Fatal("domain called before the interaction was acknowledged")
	}
	if r := f.responses[0]; r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource ||
		r.Data == nil || r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Errorf("first response = %+v, want an ephemeral deferral", r)
	}
	if len(f.followups) != 1 || f.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Errorf("followups = %+v, want one ephemeral reply", f.followups)
	}
}
