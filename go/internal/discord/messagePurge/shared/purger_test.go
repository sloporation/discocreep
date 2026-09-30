package shared

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestPlanDeletes(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	msg := func(channel snowflake.ID, age time.Duration) discord.Message {
		return discord.Message{ID: snowflake.New(now.Add(-age)), ChannelID: channel}
	}

	var msgs []discord.Message
	// Channel 1: 150 recent messages -> one batch of 100, one of 50.
	for i := range 150 {
		msgs = append(msgs, msg(1, time.Duration(i+1)*time.Minute))
	}
	// Channel 2: one recent (bulk needs 2) and two old -> all single.
	msgs = append(msgs, msg(2, time.Hour), msg(2, 20*24*time.Hour), msg(2, 30*24*time.Hour))
	// Channel 3: just under the cut-off is old (margin), so single.
	msgs = append(msgs, msg(3, 14*24*time.Hour-30*time.Minute))

	plans := planDeletes(msgs, now)

	if got := len(plans[1].bulk); got != 2 {
		t.Fatalf("channel 1 batches = %d, want 2", got)
	}
	if a, b := len(plans[1].bulk[0]), len(plans[1].bulk[1]); a+b != 150 || max(a, b) != 100 {
		t.Errorf("channel 1 batch sizes = %d, %d; want 100 and 50", a, b)
	}
	if len(plans[1].single) != 0 {
		t.Errorf("channel 1 singles = %d, want 0", len(plans[1].single))
	}
	if len(plans[2].bulk) != 0 || len(plans[2].single) != 3 {
		t.Errorf("channel 2 = %d batches, %d singles; want 0, 3", len(plans[2].bulk), len(plans[2].single))
	}
	if len(plans[3].bulk) != 0 || len(plans[3].single) != 1 {
		t.Errorf("channel 3 = %d batches, %d singles; want 0, 1", len(plans[3].bulk), len(plans[3].single))
	}
}
