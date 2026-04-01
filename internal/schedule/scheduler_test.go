package schedule_test

import (
	"context"
	"testing"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/schedule"
)

func TestScheduler_FiresOnCron(t *testing.T) {
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	schedStore := schedule.NewStore(s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output := make(chan channel.TaggedMessage, 8)

	// Create a test channel for resolution.
	testChannelID := channel.ChannelID("test-channel-id")
	channelMap := map[channel.ChannelID]channel.Channel{
		testChannelID: &fakeChannel{name: "desktop"},
	}

	scheduler := schedule.NewScheduler(schedule.SchedulerParams{
		Store:  schedStore,
		Output: output,
		Channels: func() map[channel.ChannelID]channel.Channel {
			return channelMap
		},
	})

	// Add a schedule that fires every second (cron doesn't go below minutes,
	// so we set NextRunAt to now to force an immediate fire).
	sched := schedule.Schedule{
		ID:          schedule.GenerateID(),
		CronExpr:    "* * * * *",
		Prompt:      "test prompt",
		ChannelName: "desktop",
		Status:      schedule.StatusActive,
		CreatedAt:   time.Now(),
		NextRunAt:   time.Now().Add(-1 * time.Second), // already due
	}
	if err := schedStore.Add(ctx, sched); err != nil {
		t.Fatalf("add schedule: %v", err)
	}

	go scheduler.Run(ctx)

	// Wait for the message to appear.
	select {
	case msg := <-output:
		if msg.ChannelID != testChannelID {
			t.Fatalf("expected channel ID %q, got %q", testChannelID, msg.ChannelID)
		}
		if msg.Text != "test prompt" {
			t.Fatalf("expected text 'test prompt', got %q", msg.Text)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for scheduled message")
	}

	// Store is updated before the message is sent, so it's already consistent.
	got, err := schedStore.Get(ctx, sched.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LastRunAt.IsZero() {
		t.Fatal("expected LastRunAt to be set after fire")
	}
}

func TestScheduler_SkipsPausedSchedules(t *testing.T) {
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	schedStore := schedule.NewStore(s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output := make(chan channel.TaggedMessage, 8)

	testChannelID := channel.ChannelID("test-channel-id")
	channelMap := map[channel.ChannelID]channel.Channel{
		testChannelID: &fakeChannel{name: "desktop"},
	}

	scheduler := schedule.NewScheduler(schedule.SchedulerParams{
		Store:  schedStore,
		Output: output,
		Channels: func() map[channel.ChannelID]channel.Channel {
			return channelMap
		},
	})

	// Add a paused schedule that would otherwise be due.
	sched := schedule.Schedule{
		ID:          schedule.GenerateID(),
		CronExpr:    "* * * * *",
		Prompt:      "should not fire",
		ChannelName: "desktop",
		Status:      schedule.StatusPaused,
		CreatedAt:   time.Now(),
		NextRunAt:   time.Now().Add(-1 * time.Second),
	}
	if err := schedStore.Add(ctx, sched); err != nil {
		t.Fatalf("add schedule: %v", err)
	}

	go scheduler.Run(ctx)

	// Should not receive any messages.
	select {
	case msg := <-output:
		t.Fatalf("expected no messages from paused schedule, got: %q", msg.Text)
	case <-ctx.Done():
		// Expected — timed out with no messages.
	}
}

func TestScheduler_ReloadPicksUpNewSchedules(t *testing.T) {
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	schedStore := schedule.NewStore(s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output := make(chan channel.TaggedMessage, 8)

	testChannelID := channel.ChannelID("test-channel-id")
	channelMap := map[channel.ChannelID]channel.Channel{
		testChannelID: &fakeChannel{name: "desktop"},
	}

	scheduler := schedule.NewScheduler(schedule.SchedulerParams{
		Store:  schedStore,
		Output: output,
		Channels: func() map[channel.ChannelID]channel.Channel {
			return channelMap
		},
	})

	go scheduler.Run(ctx)

	// Give the scheduler a moment to start and block on "no active schedules".
	time.Sleep(50 * time.Millisecond)

	// Add a schedule that's immediately due and reload.
	sched := schedule.Schedule{
		ID:          schedule.GenerateID(),
		CronExpr:    "* * * * *",
		Prompt:      "added after start",
		ChannelName: "desktop",
		Status:      schedule.StatusActive,
		CreatedAt:   time.Now(),
		NextRunAt:   time.Now().Add(-1 * time.Second),
	}
	if err := schedStore.Add(ctx, sched); err != nil {
		t.Fatalf("add schedule: %v", err)
	}

	scheduler.Reload()

	select {
	case msg := <-output:
		if msg.Text != "added after start" {
			t.Fatalf("expected 'added after start', got %q", msg.Text)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for reloaded schedule to fire")
	}
}

// fakeChannel is a minimal Channel implementation for testing scheduler channel resolution.
type fakeChannel struct {
	name string
}

func (f *fakeChannel) Info() channel.Info {
	return channel.Info{
		ID:   channel.ChannelID("test-channel-id"),
		Name: f.name,
		Type: channel.TypeSocket,
	}
}

func (f *fakeChannel) Messages(ctx context.Context) <-chan string {
	ch := make(chan string)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

func (f *fakeChannel) Send(_ context.Context, _ string) (channel.MessageID, error) {
	return "", nil
}

func (f *fakeChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error {
	return nil
}

func (f *fakeChannel) Done(_ context.Context) error {
	return nil
}

func (f *fakeChannel) SplitStatusMessages() bool {
	return false
}

func (f *fakeChannel) Markup() channel.Markup {
	return channel.MarkupMarkdown
}

func (f *fakeChannel) StatusWrap() channel.StatusWrap {
	return channel.StatusWrap{}
}
