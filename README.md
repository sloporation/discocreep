# discordbot

Basically completely vibe coded DiscordJS bot because lifes too short to agonize over NodeJS.

## File Structure

`src/` - contains the source code for the bot. This gets packaged and shipped.

Code is organised by function. Each function lives in `src/functions/<function_name>/` and contains:

- `src/functions/<function_name>/commands/<command>.js` - slash commands
- `src/functions/<function_name>/events/<event>.js` - event handlers
- `src/functions/<function_name>/<service>.js` - service/business logic used by that function

For example, auto voice channel code lives in:
- `src/functions/avc/commands/monitor.js` → `/avc_monitor`
- `src/functions/avc/events/channelCreate.js` → handles `channelCreate` event

Core/shared functionality (interactionCreate dispatcher, ready event, utility commands) lives in `src/functions/core/`.

## Functions

I've had to cross over the 'other bot' to here. Not all functionality is there. Not all of it pertains to this bots 'primary use'.

MRs are permitted.

- **core** - Shared infrastructure: interaction dispatcher, ready event, utility commands
- **Auto Voice Channel (avc)** - Auto voice channel creation and management commands for the channel owner
- **User Join Approval** - Posts a forum thread when a user joins; members must approve. Tracks invite link used and tags the inviter.
- **Event Management** - Tracks guild events, creates a voice channel before the event, deletes it after. Organizer has full control.
- **Channel Sync (channelSync)** - Maintains a database record of all guild channels. Tracks channel type, parent category, and managed status. Soft deletes channels removed out of band.
