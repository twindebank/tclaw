package e2etest

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/agent"
	"tclaw/internal/channel"
	"tclaw/internal/channel/outbox"
	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/store"
	"tclaw/internal/queue"
)

// CommandFunc is the signature for the mock CLI subprocess.
// Matches agent.Options.CommandFunc.
type CommandFunc = func(ctx context.Context, args []string, env []string, dir string) (stdout io.ReadCloser, wait func() error, err error)

// Config configures a test harness.
type Config struct {
	// Channels to create. Defaults to one channel named "main" (TypeSocket).
	Channels []ChannelConfig

	// CommandFunc is the mock CLI. Defaults to Respond("ok").
	CommandFunc CommandFunc

	// StoreDir overrides the temp dir (for restart tests sharing state).
	StoreDir string

	// Sessions pre-seeds session IDs per channel.
	Sessions map[string]string

	// SystemPrompt sets the base system prompt.
	SystemPrompt string

	// AllowedTools for the user.
	AllowedTools []claudecli.Tool

	// DisallowedTools for the user.
	DisallowedTools []claudecli.Tool

	// ChannelToolOverrides per channel name.
	ChannelToolOverrides map[string]agent.ChannelToolPermissions

	// DisableOutbox sends directly to channels instead of through the outbox.
	DisableOutbox bool

	// Debug logs raw CLI events for troubleshooting.
	Debug bool
}

// ChannelConfig defines a test channel.
type ChannelConfig struct {
	Name        string
	Type        channel.ChannelType // defaults to TypeSocket
	Split       bool                // SplitStatusMessages (true = Telegram-style)
	Description string
	Purpose     string
}

// SessionUpdate records a session ID change.
type SessionUpdate struct {
	ChannelID channel.ChannelID
	SessionID string
}

// TurnRecord records a turn start event.
type TurnRecord struct {
	ChannelName string
	StartedAt   time.Time
}

// Harness wires the full agent pipeline with real stores, queue, outbox,
// and activity tracker. Only the CLI subprocess is replaced.
type Harness struct {
	t             *testing.T
	channels      map[string]*TestChannel
	channelMap    map[channel.ChannelID]channel.Channel
	s             store.Store
	ob            *outbox.Outbox
	q             *queue.Queue
	activity      *channel.ActivityTracker
	runtimeState  *channel.RuntimeStateStore
	opts          agent.Options
	channelChange chan struct{}

	mu             sync.Mutex
	sessionUpdates []SessionUpdate
	turnLog        []TurnRecord

	cancelRunPtr *func()
}

// NewHarness creates a fully wired test harness.
func NewHarness(t *testing.T, cfg Config) *Harness {
	t.Helper()

	if len(cfg.Channels) == 0 {
		cfg.Channels = []ChannelConfig{{Name: "main"}}
	}
	if cfg.CommandFunc == nil {
		cfg.CommandFunc = Respond("ok")
	}

	storeDir := cfg.StoreDir
	if storeDir == "" {
		storeDir = t.TempDir()
	}
	s, err := store.NewFS(storeDir)
	require.NoError(t, err, "create store")

	// Build test channels.
	channels := make(map[string]*TestChannel, len(cfg.Channels))
	channelMap := make(map[channel.ChannelID]channel.Channel, len(cfg.Channels))
	channelNames := make([]string, 0, len(cfg.Channels))

	for _, cc := range cfg.Channels {
		tc := NewTestChannel(cc)
		channels[cc.Name] = tc
		channelMap[tc.Info().ID] = tc
		channelNames = append(channelNames, cc.Name)
	}

	// Auto-shutdown: when all channels are closed and a Done fires with an
	// empty queue, cancel the run context. The queue emptiness check prevents
	// early shutdown when system Dones (queue-ack) fire before the real turn.
	allClosed := func() bool {
		for _, tc := range channels {
			if !tc.closed {
				return false
			}
		}
		return true
	}
	var cancelRun func()
	var q *queue.Queue // set below after q is created
	for _, tc := range channels {
		tc.onDone = func() {
			if allClosed() && (q == nil || q.Len() == 0) {
				if cancelRun != nil {
					cancelRun()
				}
			}
		}
	}

	runtimeState := channel.NewRuntimeStateStore(s)
	activity := channel.NewPersistedActivityTracker(context.Background(), runtimeState, channelNames)

	// Set the queue ref used by the onDone auto-shutdown closure.
	q = queue.New(queue.QueueParams{
		Store:    s,
		Activity: activity,
		Channels: func() map[channel.ChannelID]channel.Channel { return channelMap },
	})

	channelChangeCh := make(chan struct{})

	// Build sessions map (name → channel ID).
	sessions := make(map[channel.ChannelID]string)
	for name, sid := range cfg.Sessions {
		if tc, ok := channels[name]; ok {
			sessions[tc.Info().ID] = sid
		}
	}

	// Build channel tool overrides (name → channel ID).
	var channelToolOverrides map[channel.ChannelID]agent.ChannelToolPermissions
	if len(cfg.ChannelToolOverrides) > 0 {
		channelToolOverrides = make(map[channel.ChannelID]agent.ChannelToolPermissions)
		for name, perms := range cfg.ChannelToolOverrides {
			if tc, ok := channels[name]; ok {
				channelToolOverrides[tc.Info().ID] = perms
			}
		}
	}

	h := &Harness{
		t:             t,
		channels:      channels,
		channelMap:    channelMap,
		s:             s,
		activity:      activity,
		runtimeState:  runtimeState,
		q:             q,
		channelChange: channelChangeCh,
		cancelRunPtr:  &cancelRun,
	}

	// Outbox setup.
	var ob *outbox.Outbox
	if !cfg.DisableOutbox {
		ob = outbox.New(outbox.Params{
			Store:    s,
			Channels: func() map[channel.ChannelID]channel.Channel { return channelMap },
		})
		h.ob = ob
	}

	h.opts = agent.Options{
		Channels:             channelMap,
		Sessions:             sessions,
		SystemPrompt:         cfg.SystemPrompt,
		Debug:                cfg.Debug,
		AllowedTools:         cfg.AllowedTools,
		DisallowedTools:      cfg.DisallowedTools,
		ChannelToolOverrides: channelToolOverrides,
		ChannelChangeCh:      channelChangeCh,
		Queue:                q,
		Outbox:               ob,
		CommandFunc:          cfg.CommandFunc,
		HomeDir:              t.TempDir(),
		MemoryDir:            t.TempDir(),
		OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.sessionUpdates = append(h.sessionUpdates, SessionUpdate{
				ChannelID: chID,
				SessionID: sessionID,
			})
		},
		OnTurnStart: func(channelName string) {
			// Mirror what the router's bridge goroutine does: record
			// message arrival (sets lastMessageAt) and mark processing.
			h.activity.MessageReceived(channelName)
			h.activity.TurnStarted(channelName)
			h.mu.Lock()
			defer h.mu.Unlock()
			h.turnLog = append(h.turnLog, TurnRecord{
				ChannelName: channelName,
				StartedAt:   time.Now(),
			})
		},
		OnTurnEnd: func(channelName string) {
			h.activity.TurnEnded(channelName)
		},
	}

	return h
}

// Run starts the outbox and agent loop. Blocks until ctx is cancelled, agent
// exits, or all injected messages have been processed (auto-shutdown).
func (h *Harness) Run(ctx context.Context) error {
	// Wrap the caller's context so auto-shutdown can cancel it.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	*h.cancelRunPtr = cancel

	// The outbox needs its own context so it can finish delivering after
	// the agent loop exits.
	if h.ob != nil {
		h.ob.Start(context.Background())
	}

	msgs := channel.FanIn(runCtx, h.channelMap)
	err := agent.RunWithMessages(runCtx, h.opts, msgs)

	// Flush outbox so all pending deliveries complete.
	if h.ob != nil {
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer flushCancel()
		h.ob.Flush(flushCtx)
		h.ob.Stop()
	}

	return err
}

// Channel returns the TestChannel by name.
func (h *Harness) Channel(name string) *TestChannel {
	tc, ok := h.channels[name]
	if !ok {
		h.t.Fatalf("unknown channel %q", name)
	}
	return tc
}

// SessionFor returns the last-known session ID for a channel by name.
func (h *Harness) SessionFor(name string) string {
	tc, ok := h.channels[name]
	if !ok {
		return ""
	}
	chID := tc.Info().ID
	h.mu.Lock()
	defer h.mu.Unlock()
	// Return the last update for this channel.
	for i := len(h.sessionUpdates) - 1; i >= 0; i-- {
		if h.sessionUpdates[i].ChannelID == chID {
			return h.sessionUpdates[i].SessionID
		}
	}
	return ""
}

// TurnLog returns the recorded turn start events.
func (h *Harness) TurnLog() []TurnRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]TurnRecord, len(h.turnLog))
	copy(out, h.turnLog)
	return out
}

// FlushOutbox waits for all pending outbox deliveries to complete.
func (h *Harness) FlushOutbox(ctx context.Context) {
	if h.ob != nil {
		h.ob.Flush(ctx)
	}
}

// LoadPersistedQueue loads queue state from the store (for restart tests).
func (h *Harness) LoadPersistedQueue() {
	err := h.q.LoadPersisted(context.Background())
	require.NoError(h.t, err, "load persisted queue")
}

// InjectTagged pushes a pre-built TaggedMessage into the queue. Use this
// for cross-channel, schedule, and notification messages that don't come
// from a channel's Messages() stream.
func (h *Harness) InjectTagged(msg channel.TaggedMessage) {
	if err := h.q.Push(context.Background(), msg); err != nil {
		h.t.Fatalf("InjectTagged: %v", err)
	}
}

// CloseChannelChange triggers ErrChannelChanged in the agent loop.
func (h *Harness) CloseChannelChange() {
	close(h.channelChange)
}
