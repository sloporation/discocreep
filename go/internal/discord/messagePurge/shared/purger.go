package shared

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/locks"
)

// Discord only bulk-deletes messages younger than 14 days; keep an hour of
// margin so a message doesn't age out between planning and deleting.
const bulkDeleteMaxAge = 14*24*time.Hour - time.Hour

// bulkDeleteMax is the most messages one bulk delete accepts.
const bulkDeleteMax = 100

// jobLeaseTTL is how long a job stays claimed by a worker that stops
// renewing its lease (i.e. dies) before another worker resumes it.
const jobLeaseTTL = time.Minute

// resumeInterval is how often each worker looks for unfinished jobs to pick up.
const resumeInterval = time.Minute

// Purger runs purge jobs in the background, one goroutine per job. Jobs are
// persisted in purge_jobs and each is leased to one worker while it runs, so
// with several workers a job runs exactly once, and a job whose worker dies
// is resumed by another (see ResumeLoop).
type Purger struct {
	db      *database.DB
	alerter *alerts.Alerter
	locker  *locks.Locker

	mu     sync.Mutex
	active map[int64]bool // job IDs running in this process
}

// NewPurger returns a Purger backed by db, reporting to alerter and
// coordinating with other workers through locker.
func NewPurger(db *database.DB, alerter *alerts.Alerter, locker *locks.Locker) *Purger {
	return &Purger{db: db, alerter: alerter, locker: locker, active: make(map[int64]bool)}
}

// Queue creates a purge job for the user and starts it, unless one is
// already queued or running for them in this guild, in which case that
// job's ID is returned with existing = true.
func (p *Purger) Queue(ctx context.Context, client *bot.Client, guildID, userID snowflake.ID, trigger string, requestedBy *snowflake.ID) (jobID int64, existing bool, err error) {
	err = p.db.QueryRowContext(ctx,
		"SELECT id FROM purge_jobs WHERE guild_id = ? AND user_id = ? AND status IN ('queued', 'running') LIMIT 1",
		guildID, userID,
	).Scan(&jobID)
	if err == nil {
		return jobID, true, nil
	}

	res, err := p.db.ExecContext(ctx,
		"INSERT INTO purge_jobs (guild_id, user_id, `trigger`, requested_by) VALUES (?, ?, ?, ?)",
		guildID, userID, trigger, requestedBy,
	)
	if err != nil {
		return 0, false, fmt.Errorf("create job: %w", err)
	}
	if jobID, err = res.LastInsertId(); err != nil {
		return 0, false, err
	}

	p.start(client, jobID)
	return jobID, false, nil
}

// ResumeLoop returns a start hook that picks up unfinished jobs now and
// every resumeInterval: jobs left by a previous process, or by a worker that
// died mid-job. Jobs another worker is running are skipped (their lease is
// held).
func (p *Purger) ResumeLoop(client *bot.Client) func(ctx context.Context) {
	return func(ctx context.Context) {
		ticker := time.NewTicker(resumeInterval)
		defer ticker.Stop()
		for {
			p.resume(ctx, client)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

func (p *Purger) resume(ctx context.Context, client *bot.Client) {
	rows, err := p.db.QueryContext(ctx,
		"SELECT id FROM purge_jobs WHERE status IN ('queued', 'running')",
	)
	if err != nil {
		slog.Error("messagePurge: load unfinished jobs", "err", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		p.start(client, id)
	}
}

// start runs the job in the background if this process isn't already
// running it and no other worker holds its lease.
func (p *Purger) start(client *bot.Client, jobID int64) {
	p.mu.Lock()
	if p.active[jobID] {
		p.mu.Unlock()
		return
	}
	p.active[jobID] = true
	p.mu.Unlock()
	done := func() {
		p.mu.Lock()
		delete(p.active, jobID)
		p.mu.Unlock()
	}

	lease, ok, err := p.locker.Acquire(context.Background(), fmt.Sprintf("purge:job:%d", jobID), jobLeaseTTL)
	if err != nil || !ok {
		if err != nil {
			slog.Warn("messagePurge: claim job", "id", jobID, "err", err)
		}
		done()
		return
	}

	go func() {
		defer done()
		defer lease.Release()
		p.run(client, jobID, lease.Lost())
	}()
}

// job is a purge_jobs row.
type job struct {
	id              int64
	guildID, userID snowflake.ID
	trigger         string
	requestedBy     *snowflake.ID
	deleted, failed int
}

// run deletes every message the search finds for the job's user, newest
// first, paging backwards by message ID so failures aren't retried forever.
// If lost closes (the lease was lost), it stops without finishing the job,
// leaving it for whichever worker now holds it.
func (p *Purger) run(client *bot.Client, jobID int64, lost <-chan struct{}) {
	ctx := context.Background()

	var j job
	if err := p.db.QueryRowContext(ctx,
		"SELECT id, guild_id, user_id, `trigger`, requested_by, deleted, failed FROM purge_jobs WHERE id = ?",
		jobID,
	).Scan(&j.id, &j.guildID, &j.userID, &j.trigger, &j.requestedBy, &j.deleted, &j.failed); err != nil {
		slog.Error("messagePurge: load job", "id", jobID, "err", err)
		return
	}
	if _, err := p.db.ExecContext(ctx,
		"UPDATE purge_jobs SET status = 'running', started_at = COALESCE(started_at, NOW()) WHERE id = ?",
		jobID,
	); err != nil {
		slog.Error("messagePurge: mark running", "id", jobID, "err", err)
	}
	slog.Info("messagePurge: job started", "id", jobID, "guild_id", j.guildID, "user_id", j.userID, "trigger", j.trigger)

	d := &deleter{client: client, rearchive: make(map[snowflake.ID]bool)}
	defer d.restoreArchived()

	var before snowflake.ID
	for {
		select {
		case <-lost:
			slog.Warn("messagePurge: lost job lease, stopping", "id", jobID)
			return
		default:
		}

		msgs, _, wait, err := searchPage(client, j.guildID, j.userID, before)
		if err != nil {
			p.finish(client, &j, fmt.Errorf("search messages: %w", err))
			return
		}
		if wait > 0 {
			time.Sleep(wait) // search index still building
			continue
		}
		if len(msgs) == 0 {
			break
		}

		for _, m := range msgs {
			if before == 0 || m.ID < before {
				before = m.ID
			}
		}

		deleted, failed := d.deletePlan(planDeletes(msgs, time.Now()))
		j.deleted += deleted
		j.failed += failed
		if _, err := p.db.ExecContext(ctx,
			"UPDATE purge_jobs SET deleted = ?, failed = ? WHERE id = ?",
			j.deleted, j.failed, jobID,
		); err != nil {
			slog.Error("messagePurge: save progress", "id", jobID, "err", err)
		}
	}

	p.finish(client, &j, nil)
}

// finish records the job's outcome and posts a summary to admin alerts.
func (p *Purger) finish(client *bot.Client, j *job, jobErr error) {
	status, errText := "done", (*string)(nil)
	if jobErr != nil {
		status = "failed"
		s := jobErr.Error()
		errText = &s
	}
	if _, err := p.db.ExecContext(context.Background(),
		"UPDATE purge_jobs SET status = ?, deleted = ?, failed = ?, error = ?, finished_at = NOW() WHERE id = ?",
		status, j.deleted, j.failed, errText, j.id,
	); err != nil {
		slog.Error("messagePurge: save result", "id", j.id, "err", err)
	}
	slog.Info("messagePurge: job finished", "id", j.id, "status", status, "deleted", j.deleted, "failed", j.failed, "err", jobErr)

	why := fmt.Sprintf("<@%s> left the server.", j.userID)
	if j.trigger == "command" && j.requestedBy != nil {
		why = fmt.Sprintf("Requested by <@%s>.", *j.requestedBy)
	}
	alert := alerts.Alert{
		Feature: Feature,
		Title:   "Message purge finished",
		Description: fmt.Sprintf("Deleted **%d** message(s) by <@%s>. %s\n\nThe audit log still has its copy of these messages.",
			j.deleted, j.userID, why),
	}
	if j.failed > 0 {
		alert.Description += fmt.Sprintf("\n\n**%d** message(s) couldn't be deleted, usually because the bot lacks Manage Messages in that channel.", j.failed)
	}
	if jobErr != nil {
		alert.Title = "Message purge stopped early"
		alert.Fields = []discord.EmbedField{alerts.ErrorField(jobErr)}
	}
	p.alerter.Send(client, j.guildID, alert)
}

// channelPlan is how one channel's messages will be deleted.
type channelPlan struct {
	bulk   [][]snowflake.ID // batches of 2..100 recent messages
	single []snowflake.ID   // old messages, or a lone recent one
}

// planDeletes groups messages by channel, batching those young enough for
// bulk delete and leaving the rest to be deleted one at a time.
func planDeletes(msgs []discord.Message, now time.Time) map[snowflake.ID]*channelPlan {
	recent := make(map[snowflake.ID][]snowflake.ID)
	plans := make(map[snowflake.ID]*channelPlan)
	plan := func(channelID snowflake.ID) *channelPlan {
		if plans[channelID] == nil {
			plans[channelID] = &channelPlan{}
		}
		return plans[channelID]
	}

	for _, m := range msgs {
		if now.Sub(m.ID.Time()) < bulkDeleteMaxAge {
			recent[m.ChannelID] = append(recent[m.ChannelID], m.ID)
		} else {
			plan(m.ChannelID).single = append(plan(m.ChannelID).single, m.ID)
		}
	}
	for channelID, ids := range recent {
		for len(ids) > 0 {
			n := min(len(ids), bulkDeleteMax)
			if n == 1 { // bulk delete needs at least 2
				plan(channelID).single = append(plan(channelID).single, ids[0])
			} else {
				plan(channelID).bulk = append(plan(channelID).bulk, ids[:n])
			}
			ids = ids[n:]
		}
	}
	return plans
}

// deleter carries out delete plans, unarchiving threads as needed.
type deleter struct {
	client    *bot.Client
	rearchive map[snowflake.ID]bool // threads unarchived to delete from
}

// deletePlan runs every channel's plan and returns how many messages were
// deleted and how many couldn't be.
func (d *deleter) deletePlan(plans map[snowflake.ID]*channelPlan) (deleted, failed int) {
	for channelID, plan := range plans {
		for _, batch := range plan.bulk {
			err := d.withThreadUnarchived(channelID, func() error {
				return d.client.Rest.BulkDeleteMessages(channelID, batch)
			})
			if err == nil {
				deleted += len(batch)
				continue
			}
			// Fall back to one at a time, e.g. if some were already deleted.
			slog.Warn("messagePurge: bulk delete failed, deleting individually", "channel_id", channelID, "err", err)
			plan.single = append(plan.single, batch...)
		}
		for _, id := range plan.single {
			err := d.withThreadUnarchived(channelID, func() error {
				return d.client.Rest.DeleteMessage(channelID, id)
			})
			switch {
			case err == nil, hasCode(err, rest.JSONErrorCodeUnknownMessage): // already gone
				deleted++
			default:
				slog.Warn("messagePurge: delete message", "channel_id", channelID, "message_id", id, "err", err)
				failed++
			}
		}
	}
	return deleted, failed
}

// withThreadUnarchived runs fn, and if it fails because channelID is an
// archived thread, unarchives it and tries once more. Threads are
// re-archived by restoreArchived when the job ends.
func (d *deleter) withThreadUnarchived(channelID snowflake.ID, fn func() error) error {
	err := fn()
	if !hasCode(err, rest.JSONErrorCodeOperationOnArchivedThread) {
		return err
	}
	archived := false
	if _, uerr := d.client.Rest.UpdateChannel(channelID, discord.GuildThreadUpdate{Archived: &archived}); uerr != nil {
		return err
	}
	d.rearchive[channelID] = true
	return fn()
}

// restoreArchived re-archives threads that were unarchived to delete from.
func (d *deleter) restoreArchived() {
	archived := true
	for threadID := range d.rearchive {
		if _, err := d.client.Rest.UpdateChannel(threadID, discord.GuildThreadUpdate{Archived: &archived}); err != nil {
			slog.Warn("messagePurge: re-archive thread", "thread_id", threadID, "err", err)
		}
	}
}

// hasCode reports whether err is a Discord API error with the given code.
func hasCode(err error, code rest.JSONErrorCode) bool {
	var rerr *rest.Error
	return errors.As(err, &rerr) && rerr.Code == code
}
