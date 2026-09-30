package watcher

import (
	"encoding/json"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

// A snapshot must survive the trip to a worker: marshalled by the watcher,
// parsed as a GUILD_CREATE by disgo on the worker.
func TestSnapshotRoundTrip(t *testing.T) {
	const guildID, textID, voiceID, threadID, roleID, userID = 1, 10, 11, 12, 20, 30
	voice := snowflake.ID(voiceID)

	var text discord.GuildTextChannel
	if err := json.Unmarshal([]byte(`{"id":"10","type":0,"guild_id":"1","name":"general"}`), &text); err != nil {
		t.Fatal(err)
	}
	var vc discord.GuildVoiceChannel
	if err := json.Unmarshal([]byte(`{"id":"11","type":2,"guild_id":"1","name":"Lobby"}`), &vc); err != nil {
		t.Fatal(err)
	}

	var thread discord.GuildThread
	if err := json.Unmarshal([]byte(`{"id":"12","type":11,"guild_id":"1","parent_id":"10","name":"a thread","thread_metadata":{}}`), &thread); err != nil {
		t.Fatal(err)
	}

	in := discord.GatewayGuild{
		RestGuild: discord.RestGuild{
			Guild: discord.Guild{ID: guildID, Name: "Test"},
			Roles: []discord.Role{{ID: roleID, Name: "Members"}},
		},
		Channels:    []discord.GuildChannel{text, vc},
		Threads:     []discord.GuildThread{thread},
		Members:     []discord.Member{{User: discord.User{ID: userID, Username: "alice"}}},
		VoiceStates: []discord.VoiceState{{GuildID: guildID, ChannelID: &voice, UserID: userID}},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := gateway.UnmarshalEventData(data, gateway.EventTypeGuildCreate)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := ev.(gateway.EventGuildCreate)
	if !ok {
		t.Fatalf("parsed as %T, want EventGuildCreate", ev)
	}

	if out.ID != guildID || out.Name != "Test" {
		t.Errorf("guild = %d %q", out.ID, out.Name)
	}
	if len(out.Channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(out.Channels))
	}
	if _, ok := out.Channels[1].(discord.GuildVoiceChannel); !ok {
		t.Errorf("second channel parsed as %T, want GuildVoiceChannel", out.Channels[1])
	}
	if len(out.Threads) != 1 || out.Threads[0].ID() != threadID || *out.Threads[0].ParentID() != textID {
		t.Errorf("threads = %+v", out.Threads)
	}
	if len(out.Roles) != 1 || out.Roles[0].Name != "Members" {
		t.Errorf("roles = %+v", out.Roles)
	}
	if len(out.VoiceStates) != 1 || out.VoiceStates[0].ChannelID == nil || *out.VoiceStates[0].ChannelID != voiceID {
		t.Errorf("voice states = %+v", out.VoiceStates)
	}
	if len(out.Members) != 1 || out.Members[0].User.Username != "alice" {
		t.Errorf("members = %+v", out.Members)
	}
}
