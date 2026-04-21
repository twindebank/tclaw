package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/libraries/store"
	"tclaw/internal/notification"
)

const (
	// Notification type names — agent uses these to subscribe.
	TypeNewEmail = "new_email"

	defaultPollInterval = 2 * time.Minute
	maxPollResults      = 10

	// Maximum number of recently-seen message IDs to remember per subscription.
	// Gmail's history.list can return the same message across overlapping polls
	// (the startHistoryId is inclusive, and history records can repeat), so we
	// dedupe against this rolling set to avoid re-notifying the agent.
	maxSeenMessageIDs = 500
)

// gmailPollConfig is stored in Subscription.Config (opaque to the manager).
// The history cursor is persisted separately in the state store — not here.
type gmailPollConfig struct {
	CredentialSetID string        `json:"credential_set_id"`
	Interval        time.Duration `json:"interval"`
}

// notifier implements notification.Notifier for the Google package.
type notifier struct {
	depsMap func() map[credential.CredentialSetID]Deps
	state   store.Store

	mu      sync.Mutex
	cancels map[notification.SubscriptionID]context.CancelFunc
}

func newNotifier(depsMap func() map[credential.CredentialSetID]Deps, state store.Store) *notifier {
	return &notifier{
		depsMap: depsMap,
		state:   state,
		cancels: make(map[notification.SubscriptionID]context.CancelFunc),
	}
}

// cursorKey returns the store key for a subscription's history cursor.
func cursorKey(id notification.SubscriptionID) string {
	return "gmail_cursor/" + string(id)
}

// seenKey returns the store key for a subscription's rolling set of recently-
// notified message IDs, used to silently suppress duplicate polling results.
func seenKey(id notification.SubscriptionID) string {
	return "gmail_seen/" + string(id)
}

func (n *notifier) saveCursor(ctx context.Context, id notification.SubscriptionID, historyID string) {
	if err := n.state.Set(ctx, cursorKey(id), []byte(historyID)); err != nil {
		slog.Error("gmail notifier: failed to persist cursor", "subscription", id, "error", err)
	}
}

func (n *notifier) loadCursor(ctx context.Context, id notification.SubscriptionID) string {
	data, err := n.state.Get(ctx, cursorKey(id))
	if err != nil || len(data) == 0 {
		return ""
	}
	return string(data)
}

func (n *notifier) deleteCursor(ctx context.Context, id notification.SubscriptionID) {
	if err := n.state.Delete(ctx, cursorKey(id)); err != nil {
		slog.Warn("gmail notifier: failed to delete cursor", "subscription", id, "error", err)
	}
}

// loadSeen returns the ordered list of recently-notified message IDs for this
// subscription (oldest first). Returns an empty slice on first use or on
// decode failure — a missing/corrupt seen set should never block notifications.
func (n *notifier) loadSeen(ctx context.Context, id notification.SubscriptionID) []string {
	data, err := n.state.Get(ctx, seenKey(id))
	if err != nil || len(data) == 0 {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		slog.Warn("gmail notifier: failed to decode seen set, starting fresh",
			"subscription", id, "error", err)
		return nil
	}
	return ids
}

func (n *notifier) saveSeen(ctx context.Context, id notification.SubscriptionID, ids []string) {
	data, err := json.Marshal(ids)
	if err != nil {
		slog.Error("gmail notifier: failed to encode seen set", "subscription", id, "error", err)
		return
	}
	if err := n.state.Set(ctx, seenKey(id), data); err != nil {
		slog.Error("gmail notifier: failed to persist seen set", "subscription", id, "error", err)
	}
}

func (n *notifier) deleteSeen(ctx context.Context, id notification.SubscriptionID) {
	if err := n.state.Delete(ctx, seenKey(id)); err != nil {
		slog.Warn("gmail notifier: failed to delete seen set", "subscription", id, "error", err)
	}
}

func (n *notifier) NotificationTypes() []notification.NotificationType {
	return []notification.NotificationType{
		{
			Name:        TypeNewEmail,
			Description: "Watch for new emails using Gmail's history API. Polls every 2 minutes for changes since the last check — only new arrivals trigger a notification, not existing unread mail.",
			Scopes:      []notification.Scope{notification.ScopeCredential, notification.ScopePersistent},
		},
	}
}

func (n *notifier) Subscribe(ctx context.Context, params notification.SubscribeParams, emitter notification.Emitter) (*notification.SubscribeResult, error) {
	if params.TypeName != TypeNewEmail {
		return nil, fmt.Errorf("unknown notification type %q", params.TypeName)
	}

	config := gmailPollConfig{
		CredentialSetID: params.CredentialSetID,
		Interval:        defaultPollInterval,
	}

	// Seed the history cursor so we only notify about messages arriving
	// after subscribe — not existing mail.
	historyID, err := n.fetchCurrentHistoryID(ctx, config.CredentialSetID)
	if err != nil {
		slog.Warn("gmail notifier: failed to seed history ID, will retry on first poll",
			"error", err)
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal gmail poll config: %w", err)
	}

	sub := notification.Subscription{
		ID:              notification.GenerateID(),
		Scope:           params.Scope,
		ChannelName:     params.ChannelName,
		PackageName:     "google",
		TypeName:        TypeNewEmail,
		Config:          configJSON,
		CredentialSetID: params.CredentialSetID,
		Label:           params.Label,
		CreatedAt:       time.Now(),
	}

	// Persist initial cursor if we got one.
	if historyID != "" {
		n.saveCursor(ctx, sub.ID, historyID)
	}

	cancel := n.startPolling(ctx, sub.ID, config, emitter)

	return &notification.SubscribeResult{
		Subscription: sub,
		Cancel:       cancel,
	}, nil
}

func (n *notifier) Resubscribe(ctx context.Context, sub notification.Subscription, emitter notification.Emitter) (notification.CancelFunc, error) {
	var config gmailPollConfig
	if err := json.Unmarshal(sub.Config, &config); err != nil {
		return nil, fmt.Errorf("parse gmail poll config: %w", err)
	}
	return n.startPolling(ctx, sub.ID, config, emitter), nil
}

func (n *notifier) Cancel(id notification.SubscriptionID) {
	n.mu.Lock()
	cancel, ok := n.cancels[id]
	if ok {
		delete(n.cancels, id)
	}
	n.mu.Unlock()

	if ok {
		cancel()
	}

	n.deleteCursor(context.Background(), id)
	n.deleteSeen(context.Background(), id)
}

func (n *notifier) startPolling(ctx context.Context, id notification.SubscriptionID, config gmailPollConfig, emitter notification.Emitter) notification.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	n.mu.Lock()
	n.cancels[id] = cancel
	n.mu.Unlock()

	go n.pollLoop(ctx, id, config, emitter)

	return func() {
		cancel()
		n.mu.Lock()
		delete(n.cancels, id)
		n.mu.Unlock()
	}
}

func (n *notifier) pollLoop(ctx context.Context, id notification.SubscriptionID, config gmailPollConfig, emitter notification.Emitter) {
	// Load persisted cursor from state store.
	cursor := n.loadCursor(ctx, id)

	// If we don't have a cursor yet, try to seed from the Gmail profile.
	if cursor == "" {
		seeded, err := n.fetchCurrentHistoryID(ctx, config.CredentialSetID)
		if err != nil {
			slog.Warn("gmail notifier: failed to fetch initial history ID, will retry",
				"subscription", id, "error", err)
		} else {
			cursor = seeded
			n.saveCursor(ctx, id, cursor)
			slog.Info("gmail notifier: seeded history ID", "subscription", id, "history_id", cursor)
		}
	}

	// Poll immediately so resubscriptions after a restart don't wait a full interval.
	cursor = n.poll(ctx, id, config.CredentialSetID, cursor, emitter)

	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cursor = n.poll(ctx, id, config.CredentialSetID, cursor, emitter)
		}
	}
}

// poll checks for new messages since the cursor using Gmail's history.list API.
// Returns the updated cursor.
func (n *notifier) poll(ctx context.Context, id notification.SubscriptionID, credSetID, cursor string, emitter notification.Emitter) string {
	if cursor == "" {
		// Still no cursor — try to seed.
		seeded, err := n.fetchCurrentHistoryID(ctx, credSetID)
		if err != nil {
			slog.Error("gmail notifier: cannot fetch history ID", "subscription", id, "error", err)
			return cursor
		}
		n.saveCursor(ctx, id, seeded)
		slog.Info("gmail notifier: seeded history ID on poll", "subscription", id, "history_id", seeded)
		return seeded
	}

	newMessageIDs, newHistoryID, err := n.fetchHistory(ctx, credSetID, cursor)
	if err != nil {
		slog.Error("gmail notifier: history fetch failed", "subscription", id, "error", err)
		return cursor
	}

	if newHistoryID != "" {
		cursor = newHistoryID
		n.saveCursor(ctx, id, cursor)
	}

	if len(newMessageIDs) == 0 {
		return cursor
	}

	// Filter out message IDs we've already notified on in a previous poll.
	// Gmail's history API can return overlapping results across polls, and
	// the cursor isn't always sufficient on its own to prevent re-notification.
	seen := n.loadSeen(ctx, id)
	freshMessageIDs, duplicates := filterSeen(newMessageIDs, seen)

	if duplicates > 0 {
		slog.Debug("gmail notifier: suppressed duplicate messages",
			"subscription", id, "duplicates", duplicates, "fresh", len(freshMessageIDs))
	}

	if len(freshMessageIDs) == 0 {
		return cursor
	}

	slog.Debug("gmail notifier: poll complete",
		"subscription", id, "new_messages", len(freshMessageIDs), "cursor", cursor)

	summaries := n.fetchMetadata(ctx, credSetID, freshMessageIDs)
	if len(summaries) == 0 {
		return cursor
	}

	// Mark these IDs as seen before emitting so a crash after emit doesn't
	// cause a re-notification on restart. A crash before emit is acceptable —
	// the cursor stays unchanged and the next poll will retry.
	n.saveSeen(ctx, id, appendCapped(seen, freshMessageIDs, maxSeenMessageIDs))

	text := formatNewEmailNotification(summaries)
	if err := emitter.Emit(ctx, notification.Notification{
		SubscriptionID: id,
		Text:           text,
	}); err != nil {
		slog.Error("gmail notifier: emit failed", "subscription", id, "error", err)
	}

	return cursor
}

// filterSeen splits candidate message IDs into those not present in seen
// (returned in order) and a count of duplicates found.
func filterSeen(candidates, seen []string) ([]string, int) {
	if len(seen) == 0 {
		return candidates, 0
	}
	seenSet := make(map[string]struct{}, len(seen))
	for _, id := range seen {
		seenSet[id] = struct{}{}
	}
	fresh := make([]string, 0, len(candidates))
	duplicates := 0
	for _, id := range candidates {
		if _, ok := seenSet[id]; ok {
			duplicates++
			continue
		}
		fresh = append(fresh, id)
	}
	return fresh, duplicates
}

// appendCapped appends newIDs to existing and trims from the front so the
// result holds at most max entries (oldest first). Callers must pass only
// previously-unseen newIDs.
func appendCapped(existing, newIDs []string, max int) []string {
	combined := append(existing, newIDs...)
	if len(combined) <= max {
		return combined
	}
	return combined[len(combined)-max:]
}

func (n *notifier) fetchCurrentHistoryID(ctx context.Context, credSetID string) (string, error) {
	depsMap := n.depsMap()
	deps, err := resolveDeps(depsMap, credSetID)
	if err != nil {
		return "", fmt.Errorf("resolve credential set %s: %w", credSetID, err)
	}

	output, err := runGWS(ctx, deps, gws.Command{
		Args:   []string{"gmail", "users", "getProfile"},
		Params: map[string]any{"userId": "me"},
	})
	if err != nil {
		return "", fmt.Errorf("get gmail profile: %w", err)
	}

	var profile struct {
		HistoryID string `json:"historyId"`
	}
	if err := json.Unmarshal(output, &profile); err != nil {
		return "", fmt.Errorf("parse gmail profile: %w", err)
	}
	if profile.HistoryID == "" {
		return "", fmt.Errorf("gmail profile returned empty historyId")
	}

	return profile.HistoryID, nil
}

func (n *notifier) fetchHistory(ctx context.Context, credSetID, startHistoryID string) ([]string, string, error) {
	depsMap := n.depsMap()
	deps, err := resolveDeps(depsMap, credSetID)
	if err != nil {
		return nil, "", fmt.Errorf("resolve credential set %s: %w", credSetID, err)
	}

	output, err := runGWS(ctx, deps, gws.Command{
		Args: []string{"gmail", "users", "history", "list"},
		Params: map[string]any{
			"userId":         "me",
			"startHistoryId": startHistoryID,
			"historyTypes":   "messageAdded",
			"maxResults":     maxPollResults,
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("list history: %w", err)
	}

	var rsp historyListResponse
	if err := json.Unmarshal(output, &rsp); err != nil {
		return nil, "", fmt.Errorf("parse history response: %w", err)
	}

	// Deduplicate message IDs — the same message can appear in multiple
	// history records.
	seen := make(map[string]bool)
	var messageIDs []string
	for _, record := range rsp.History {
		for _, added := range record.MessagesAdded {
			msgID := added.Message.ID
			if !seen[msgID] {
				seen[msgID] = true
				messageIDs = append(messageIDs, msgID)
			}
		}
	}

	return messageIDs, rsp.HistoryID, nil
}

type historyListResponse struct {
	History   []historyRecord `json:"history"`
	HistoryID string          `json:"historyId"`
}

type historyRecord struct {
	MessagesAdded []messageAddedEvent `json:"messagesAdded"`
}

type messageAddedEvent struct {
	Message struct {
		ID string `json:"id"`
	} `json:"message"`
}

func (n *notifier) fetchMetadata(ctx context.Context, credSetID string, messageIDs []string) []gmailSummary {
	depsMap := n.depsMap()
	deps, err := resolveDeps(depsMap, credSetID)
	if err != nil {
		slog.Error("gmail notifier: resolve deps for metadata fetch", "error", err)
		return nil
	}

	type result struct {
		index   int
		summary gmailSummary
		err     error
	}

	results := make([]result, len(messageIDs))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, gmailMetadataConcurrency)

	for i, msgID := range messageIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			output, fetchErr := runGWS(ctx, deps, gws.Gmail.GetMessage(map[string]any{
				"userId":          "me",
				"id":              id,
				"format":          "metadata",
				"metadataHeaders": "Subject,From,To,Date",
			}))
			if fetchErr != nil {
				results[idx] = result{index: idx, err: fetchErr}
				return
			}

			var meta gmailMessageMetadata
			if parseErr := json.Unmarshal(output, &meta); parseErr != nil {
				results[idx] = result{index: idx, err: parseErr}
				return
			}

			results[idx] = result{index: idx, summary: extractSummary(meta)}
		}(i, msgID)
	}
	wg.Wait()

	summaries := make([]gmailSummary, 0, len(results))
	for _, r := range results {
		if r.err != nil {
			slog.Warn("gmail notifier: metadata fetch failed", "error", r.err)
			continue
		}
		summaries = append(summaries, r.summary)
	}
	return summaries
}

func formatNewEmailNotification(summaries []gmailSummary) string {
	if len(summaries) == 1 {
		s := summaries[0]
		return fmt.Sprintf("📧 New email from %s: %s\n%s", s.From, s.Subject, s.Snippet)
	}

	text := fmt.Sprintf("📧 %d new emails:\n", len(summaries))
	for _, s := range summaries {
		text += fmt.Sprintf("• %s — %s\n", s.From, s.Subject)
	}
	return text
}
