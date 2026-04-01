package notification

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"tclaw/channel"
)

// ManagerParams holds configuration for creating a Manager.
type ManagerParams struct {
	Store    *Store
	Output   chan<- channel.TaggedMessage
	Channels func() map[channel.ChannelID]channel.Channel

	// Ready is an optional signal channel. If set, Run() waits for it to close
	// before loading persisted subscriptions. This ensures notifiers are
	// registered (via credential system init) before resubscription attempts.
	Ready <-chan struct{}
}

// Manager is the notification system's central orchestrator. It persists
// subscriptions, delegates to package Notifiers for mechanism-specific logic,
// and routes emitted notifications to channels. Runs at user lifetime.
//
// The manager does not handle busy-channel queueing — that's the unified
// queue's responsibility. Notifications are delivered to the output channel
// immediately; the queue decides when to process them based on source priority.
type Manager struct {
	store    *Store
	output   chan<- channel.TaggedMessage
	channels func() map[channel.ChannelID]channel.Channel
	ready    <-chan struct{}

	mu        sync.Mutex
	notifiers map[string]Notifier           // package_name -> notifier
	cancels   map[SubscriptionID]CancelFunc // active subscription cancellers
}

// NewManager creates a Manager from the given params.
func NewManager(p ManagerParams) *Manager {
	return &Manager{
		store:     p.Store,
		output:    p.Output,
		channels:  p.Channels,
		ready:     p.Ready,
		notifiers: make(map[string]Notifier),
		cancels:   make(map[SubscriptionID]CancelFunc),
	}
}

// RegisterNotifier adds a package's notifier for discoverability and delegation.
func (m *Manager) RegisterNotifier(packageName string, notifier Notifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifiers[packageName] = notifier
}

// UnregisterNotifier removes a package's notifier (e.g. when credentials are revoked).
func (m *Manager) UnregisterNotifier(packageName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.notifiers, packageName)
}

// Run waits for the ready signal (notifiers registered), loads persisted
// subscriptions, restarts their watchers, and blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	slog.Info("notification manager: started, waiting for notifiers")

	select {
	case <-m.ready:
	case <-ctx.Done():
		return
	}

	m.resubscribeAll(ctx)

	<-ctx.Done()
	m.cancelAll()
}

// Subscribe delegates to the package's Notifier, persists the subscription,
// and stores the cancel func. Returns the existing subscription if an identical
// one already exists (same package, type, channel, scope, credential set) to
// prevent duplicates across restarts.
func (m *Manager) Subscribe(ctx context.Context, packageName string, params SubscribeParams) (*SubscribeResult, error) {
	m.mu.Lock()
	notifier, ok := m.notifiers[packageName]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no notifier registered for package %q", packageName)
	}

	existing, err := m.store.FindExisting(ctx, packageName, params.TypeName, params.ChannelName, params.Scope, params.CredentialSetID)
	if err != nil {
		return nil, fmt.Errorf("check for existing subscription: %w", err)
	}
	if existing != nil {
		// Duplicate — return the existing subscription without creating a new one.
		slog.Info("notification manager: duplicate subscribe ignored, returning existing",
			"id", existing.ID, "package", packageName, "type", params.TypeName, "channel", params.ChannelName)
		return &SubscribeResult{Subscription: *existing}, nil
	}

	em := m.emitterFor(params.ChannelName, params.Scope, params.Label)
	result, err := notifier.Subscribe(ctx, params, em)
	if err != nil {
		return nil, fmt.Errorf("subscribe via %s: %w", packageName, err)
	}

	if err := m.store.Add(ctx, result.Subscription); err != nil {
		if result.Cancel != nil {
			result.Cancel()
		}
		return nil, fmt.Errorf("persist subscription: %w", err)
	}

	// If the notifier emitted synchronously during Subscribe() and the scope
	// is one-shot, the emitter couldn't remove it (not yet persisted). Clean up now.
	em.mu.Lock()
	alreadyDelivered := em.delivered
	em.mu.Unlock()
	if alreadyDelivered && params.Scope == ScopeOneShot {
		if result.Cancel != nil {
			result.Cancel()
		}
		if err := m.store.Remove(ctx, result.Subscription.ID); err != nil {
			slog.Error("notification: failed to remove one-shot after sync delivery",
				"subscription", result.Subscription.ID, "error", err)
		}
	} else {
		m.mu.Lock()
		m.cancels[result.Subscription.ID] = result.Cancel
		m.mu.Unlock()
	}

	slog.Info("notification manager: subscribed",
		"id", result.Subscription.ID,
		"package", packageName,
		"type", params.TypeName,
		"channel", params.ChannelName,
		"scope", params.Scope,
	)

	return result, nil
}

// Unsubscribe stops watching and removes the subscription.
func (m *Manager) Unsubscribe(ctx context.Context, id SubscriptionID) error {
	m.cancelOne(id)

	if err := m.store.Remove(ctx, id); err != nil {
		return fmt.Errorf("remove subscription: %w", err)
	}

	slog.Info("notification manager: unsubscribed", "id", id)
	return nil
}

// UnsubscribeByCredentialSet removes all subscriptions for a credential set.
func (m *Manager) UnsubscribeByCredentialSet(ctx context.Context, credentialSetID string) error {
	removed, err := m.store.RemoveByCredentialSet(ctx, credentialSetID)
	if err != nil {
		return fmt.Errorf("remove subscriptions for credential set %q: %w", credentialSetID, err)
	}

	for _, sub := range removed {
		m.cancelOne(sub.ID)
		slog.Info("notification manager: unsubscribed (credential set removed)",
			"id", sub.ID, "credential_set", credentialSetID)
	}
	return nil
}

// List returns all active subscriptions.
func (m *Manager) List(ctx context.Context) ([]Subscription, error) {
	return m.store.List(ctx)
}

// AvailableTypes returns all declared notification types across packages.
func (m *Manager) AvailableTypes() map[string][]NotificationType {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string][]NotificationType, len(m.notifiers))
	for name, notifier := range m.notifiers {
		result[name] = notifier.NotificationTypes()
	}
	return result
}

// resubscribeAll loads persisted subscriptions and restarts their watchers.
func (m *Manager) resubscribeAll(ctx context.Context) {
	subs, err := m.store.List(ctx)
	if err != nil {
		slog.Error("notification manager: failed to load subscriptions for resubscribe", "error", err)
		return
	}

	for _, sub := range subs {
		m.mu.Lock()
		notifier, ok := m.notifiers[sub.PackageName]
		m.mu.Unlock()
		if !ok {
			slog.Warn("notification manager: no notifier for persisted subscription, skipping",
				"id", sub.ID, "package", sub.PackageName)
			continue
		}

		emitter := m.emitterFor(sub.ChannelName, sub.Scope, sub.Label)
		cancel, err := notifier.Resubscribe(ctx, sub, emitter)
		if err != nil {
			slog.Error("notification manager: failed to resubscribe",
				"id", sub.ID, "package", sub.PackageName, "error", err)
			continue
		}

		m.mu.Lock()
		m.cancels[sub.ID] = cancel
		m.mu.Unlock()

		slog.Info("notification manager: resubscribed",
			"id", sub.ID, "package", sub.PackageName, "type", sub.TypeName)
	}
}

// emitterFor creates an Emitter that delivers to the given channel.
func (m *Manager) emitterFor(channelName string, scope Scope, label string) *emitter {
	return &emitter{
		manager:     m,
		channelName: channelName,
		scope:       scope,
		label:       label,
	}
}

type emitter struct {
	manager     *Manager
	channelName string
	scope       Scope
	label       string

	// delivered tracks whether this emitter has fired. Used by Manager.Subscribe
	// to handle one-shots that emit synchronously during Subscribe() before
	// the subscription is persisted.
	mu        sync.Mutex
	delivered bool
}

func (e *emitter) Emit(ctx context.Context, n Notification) error {
	e.mu.Lock()
	e.delivered = true
	e.mu.Unlock()

	e.manager.deliver(ctx, n.SubscriptionID, e.channelName, n.Text, e.scope, e.label)
	return nil
}

// deliver resolves the channel and sends the notification as a TaggedMessage.
// The unified queue handles busy-channel awareness — we just send immediately.
func (m *Manager) deliver(ctx context.Context, subID SubscriptionID, channelName, text string, scope Scope, label string) {
	channelID, ok := m.resolveChannel(channelName)
	if !ok {
		slog.Warn("notification: cannot resolve channel, dropping notification",
			"subscription", subID, "channel", channelName)
		return
	}

	msg := channel.TaggedMessage{
		ChannelID: channelID,
		Text:      text,
		SourceInfo: &channel.MessageSourceInfo{
			Source:            channel.SourceNotification,
			SubscriptionID:    string(subID),
			SubscriptionLabel: label,
		},
	}

	select {
	case m.output <- msg:
		slog.Info("notification: delivered",
			"subscription", subID, "channel", channelName)
	default:
		slog.Warn("notification: output buffer full, blocking",
			"subscription", subID, "channel", channelName)
		select {
		case m.output <- msg:
		case <-ctx.Done():
			return
		}
	}

	// One-shot subscriptions auto-remove after delivery.
	if scope == ScopeOneShot {
		m.cancelOne(subID)
		if err := m.store.Remove(ctx, subID); err != nil {
			slog.Debug("notification: one-shot removal after delivery",
				"subscription", subID, "error", err)
		}
	}
}

// resolveChannel finds a channel ID by name from the current channel map.
func (m *Manager) resolveChannel(name string) (channel.ChannelID, bool) {
	for _, ch := range m.channels() {
		if ch.Info().Name == name {
			return ch.Info().ID, true
		}
	}
	return "", false
}

// cancelOne stops a single subscription's watcher.
func (m *Manager) cancelOne(id SubscriptionID) {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	if ok {
		delete(m.cancels, id)
	}
	m.mu.Unlock()

	if ok && cancel != nil {
		cancel()
	}
}

// cancelAll stops all active watchers.
func (m *Manager) cancelAll() {
	m.mu.Lock()
	cancels := m.cancels
	m.cancels = make(map[SubscriptionID]CancelFunc)
	m.mu.Unlock()

	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}
