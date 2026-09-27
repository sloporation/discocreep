package commands

import (
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/shared"
)

// HandleSetupAutocomplete returns the autocomplete handler for /endorsement
// setup's tag option: it lists the tags of the forum chosen in the channel
// option. Each choice's value is the tag ID.
func HandleSetupAutocomplete() handler.AutocompleteHandler {
	return func(e *handler.AutocompleteEvent) error {
		if e.Data.Focused().Name != "tag" {
			return e.AutocompleteResult(nil)
		}

		forumID, ok := e.Data.OptSnowflake("channel")
		if !ok {
			return e.AutocompleteResult(nil) // channel not picked yet
		}
		tags, err := shared.ForumTags(e.Client(), forumID)
		if err != nil {
			slog.Debug("communityEndorsement: autocomplete forum tags", "channel_id", forumID, "err", err)
			return e.AutocompleteResult(nil) // not a forum
		}

		typed := strings.ToLower(e.Data.String("tag"))
		choices := make([]discord.AutocompleteChoice, 0, len(tags))
		for _, t := range tags {
			if typed != "" && !strings.Contains(strings.ToLower(t.Name), typed) {
				continue
			}
			choices = append(choices, discord.AutocompleteChoiceString{Name: t.Name, Value: t.ID.String()})
			if len(choices) == 25 { // Discord's limit
				break
			}
		}
		return e.AutocompleteResult(choices)
	}
}
