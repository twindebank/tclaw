package schedule

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"tclaw/internal/channel"
)

// SchedulerParams holds configuration for creating a Scheduler.
type SchedulerParams struct {
	Store    *Store
	Output   chan<- channel.TaggedMessage
	Channels func() map[channel.ChannelID]channel.Channel
}

// Scheduler runs a timer loop that fires scheduled prompts into a message channel.
// It outlives the agent — it runs at user lifetime in the router, not per-session.
type Scheduler struct {
	store    *Store
	output   chan<- channel.TaggedMessage
	reload   chan struct{}
	channels func() map[channel.ChannelID]channel.Channel
}

// NewScheduler creates a scheduler from the given params.
func NewScheduler(p SchedulerParams) *Scheduler {
	return &Scheduler{
		store:    p.Store,
		output:   p.Output,
		reload:   make(chan struct{}, 1),
		channels: p.Channels,
	}
}

// Reload signals the scheduler to re-read schedules from the store.
// Non-blocking — if a reload is already pending, this is a no-op.
func (s *Scheduler) Reload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled. It loads active schedules, sleeps until
// the earliest fires, then injects the prompt into the output channel.
func (s *Scheduler) Run(ctx context.Context) {
	slog.Info("scheduler: started")
	for {
		schedules, err := s.store.List(ctx)
		if err != nil {
			slog.Error("scheduler: failed to load schedules", "err", err)
			// Wait for reload or shutdown before retrying.
			select {
			case <-ctx.Done():
				return
			case <-s.reload:
				continue
			}
		}

		// Find the earliest NextRunAt among active schedules.
		var earliest time.Time
		for _, sched := range schedules {
			if sched.Status != StatusActive {
				continue
			}
			next := s.computeNextRun(sched)
			if earliest.IsZero() || next.Before(earliest) {
				earliest = next
			}
		}

		if earliest.IsZero() {
			// No active schedules — block until reload or shutdown.
			slog.Debug("scheduler: no active schedules, waiting for reload")
			select {
			case <-ctx.Done():
				return
			case <-s.reload:
				continue
			}
		}

		waitDuration := time.Until(earliest)
		if waitDuration < 0 {
			waitDuration = 0
		}

		slog.Debug("scheduler: sleeping until next fire", "next", earliest, "wait", waitDuration)

		timer := time.NewTimer(waitDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.reload:
			timer.Stop()
			continue
		case <-timer.C:
		}

		// Fire all schedules whose NextRunAt is at or before now.
		s.fireReadySchedules(ctx)
	}
}

// fireReadySchedules checks all active schedules and fires any that are due.
func (s *Scheduler) fireReadySchedules(ctx context.Context) {
	schedules, err := s.store.List(ctx)
	if err != nil {
		slog.Error("scheduler: failed to load schedules for firing", "err", err)
		return
	}

	now := time.Now()
	channelMap := s.channels()

	for _, sched := range schedules {
		if sched.Status != StatusActive {
			continue
		}

		next := s.computeNextRun(sched)
		if next.After(now) {
			continue
		}

		// Resolve channel name to ID.
		channelID, ok := s.resolveChannel(channelMap, sched.ChannelName)
		if !ok {
			slog.Warn("scheduler: cannot resolve channel for schedule, skipping",
				"schedule", sched.ID, "channel_name", sched.ChannelName)
			continue
		}

		// Update the store before sending so it's consistent when the consumer reads the fired message.
		// One-shot schedules are removed; recurring schedules have their run times updated.
		fireTime := now
		if sched.Once {
			if removeErr := s.store.Remove(ctx, sched.ID); removeErr != nil {
				slog.Error("scheduler: failed to remove one-shot schedule after fire", "schedule", sched.ID, "err", removeErr)
			}
		} else {
			updateErr := s.store.Update(ctx, sched.ID, func(existing *Schedule) {
				existing.LastRunAt = fireTime
				existing.NextRunAt = s.computeNextRunFromTime(existing.CronExpr, fireTime, existing.Timezone)
			})
			if updateErr != nil {
				slog.Error("scheduler: failed to update schedule after fire", "schedule", sched.ID, "err", updateErr)
			}
		}

		// Inject the prompt as a tagged message.
		msg := channel.TaggedMessage{
			ChannelID: channelID,
			Text:      sched.Prompt,
			SourceInfo: &channel.MessageSourceInfo{
				Source:       channel.SourceSchedule,
				ScheduleName: string(sched.ID),
			},
		}
		select {
		case s.output <- msg:
			slog.Info("scheduler: fired schedule", "schedule", sched.ID, "channel", sched.ChannelName)
		default:
			// Buffer full — log before blocking so we can diagnose delays.
			slog.Warn("scheduler: output buffer full, blocking", "schedule", sched.ID, "channel", sched.ChannelName)
			select {
			case s.output <- msg:
				slog.Info("scheduler: fired schedule (after buffer wait)", "schedule", sched.ID, "channel", sched.ChannelName)
			case <-ctx.Done():
				return
			}
		}
	}
}

// resolveChannel finds a channel ID by name from the current channel map.
func (s *Scheduler) resolveChannel(channelMap map[channel.ChannelID]channel.Channel, name string) (channel.ChannelID, bool) {
	if channelMap == nil {
		return "", false
	}
	for id, ch := range channelMap {
		if ch.Info().Name == name {
			return id, true
		}
	}
	return "", false
}

// computeNextRun calculates the next fire time for a schedule.
// If NextRunAt is set (even if in the past), it's used as-is — a past value means
// the schedule is overdue and should fire immediately. Otherwise computes from now.
func (s *Scheduler) computeNextRun(sched Schedule) time.Time {
	if !sched.NextRunAt.IsZero() {
		return sched.NextRunAt
	}
	return s.computeNextRunFromTime(sched.CronExpr, time.Now(), sched.Timezone)
}

// computeNextRunFromTime parses the cron expression and returns the next time after t,
// evaluated in the given IANA timezone (e.g. "Europe/London"). Empty timezone uses the system default.
func (s *Scheduler) computeNextRunFromTime(cronExpr string, t time.Time, timezone string) time.Time {
	loc, err := resolveLocation(timezone)
	if err != nil {
		slog.Error("scheduler: invalid timezone, falling back to system default", "timezone", timezone, "err", err)
		loc = time.Local
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		slog.Error("scheduler: invalid cron expression", "cron", cronExpr, "err", err)
		// Return far future to effectively disable this schedule until fixed.
		return t.Add(24 * 365 * time.Hour)
	}
	// Evaluate the cron expression in the target timezone so that expressions
	// like "0 9 * * *" mean 9am in that timezone, not 9am UTC.
	return sched.Next(t.In(loc))
}

// resolveLocation returns the time.Location for an IANA timezone name.
// Empty string returns time.Local (system default).
func resolveLocation(timezone string) (*time.Location, error) {
	if timezone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(timezone)
}
