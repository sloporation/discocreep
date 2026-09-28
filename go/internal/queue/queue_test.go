package queue

import (
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

func TestGuildIDFromPayload(t *testing.T) {
	tests := []struct {
		name, eventType, data string
		want                  snowflake.ID
	}{
		{"guild create uses id", "GUILD_CREATE", `{"id":"123","name":"x"}`, 123},
		{"guild delete uses id", "GUILD_DELETE", `{"id":"123","unavailable":true}`, 123},
		{"message uses guild_id", "MESSAGE_CREATE", `{"id":"999","guild_id":"456","channel_id":"1"}`, 456},
		{"interaction uses guild_id", "INTERACTION_CREATE", `{"id":"999","guild_id":"789","token":"t"}`, 789},
		{"DM has no guild", "MESSAGE_CREATE", `{"id":"999","channel_id":"1"}`, 0},
		{"garbage", "MESSAGE_CREATE", `not json`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GuildIDFromPayload(tt.eventType, []byte(tt.data)); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPartitionIsStable(t *testing.T) {
	const partitions = 16
	guild := snowflake.ID(1553730241736876082)
	p := Partition(guild, partitions)
	if p < 0 || p >= partitions {
		t.Fatalf("partition %d out of range", p)
	}
	for range 10 {
		if Partition(guild, partitions) != p {
			t.Fatal("partition changed between calls")
		}
	}
	if Partition(0, partitions) != 0 {
		t.Error("events without a guild should go to partition 0")
	}
}
