package shared

import (
	"net/http"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// searchGuildMessages is Discord's guild message search, which disgo doesn't wrap.
var searchGuildMessages = rest.NewEndpoint(http.MethodGet, "/guilds/{guild.id}/messages/search")

// searchPageSize is the most results Discord returns per search request.
const searchPageSize = 25

// indexNotReady is the code Discord returns (with HTTP 202) while the
// guild's search index is still being built.
const indexNotReady = 110000

type searchResponse struct {
	// Each hit is an array holding the matching message.
	Messages     [][]discord.Message `json:"messages"`
	TotalResults int                 `json:"total_results"`
	// Set on a 202 "index not yet available" response.
	Code       int     `json:"code"`
	RetryAfter float64 `json:"retry_after"`
}

// searchPage returns up to searchPageSize of the user's messages in the
// guild older than beforeID (0 = newest first), plus the total match count.
// If the search index isn't ready, it returns a non-zero wait instead.
func searchPage(client *bot.Client, guildID, userID, beforeID snowflake.ID) (msgs []discord.Message, total int, wait time.Duration, err error) {
	query := discord.QueryValues{
		"author_id":    userID,
		"include_nsfw": true,
		"sort_by":      "timestamp",
		"sort_order":   "desc",
		"limit":        searchPageSize,
	}
	if beforeID != 0 {
		query["max_id"] = beforeID
	}

	var rs searchResponse
	if err := client.Rest.Do(searchGuildMessages.Compile(query, guildID), nil, &rs); err != nil {
		return nil, 0, 0, err
	}
	if rs.Code == indexNotReady || (rs.Messages == nil && rs.RetryAfter > 0) {
		wait = time.Duration(rs.RetryAfter * float64(time.Second))
		return nil, 0, max(wait, time.Second), nil
	}

	for _, hit := range rs.Messages {
		for _, m := range hit {
			if m.Author.ID == userID {
				msgs = append(msgs, m)
			}
		}
	}
	return msgs, rs.TotalResults, 0, nil
}
