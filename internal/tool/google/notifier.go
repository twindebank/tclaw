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

	// historyPageSize is how many history records to request per page. Gmail
	// allows up to 500; 100 keeps individual responses small while making it
	// rare to need more than one page per poll.
	historyPageSize = 100

	// maxHistoryPages bounds pagination so a runaway backlog (or an API quirk
	// that never clears nextPageToken) can't loop forever in one poll.
	maxHistoryPages = 25

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

	// memoryDir is the agent-readable directory where full email bodies are
	// written (see notifier_body.go). Empty disables body files.
	memoryDir string

	// run executes a gws command; defaults to runGWS. Overridable in tests to
	// stub the Gmail API without spawning the gws binary.
	run gwsRunner

	mu      sync.Mutex
	cancels map[notification.SubscriptionID]context.CancelFunc

	// seenMu serializes the read-modify-write cycle on the per-credential
	// seen set. Multiple subscriptions can run concurrently against the same
	// Gmail account; without this lock, two pollers could both observe an
	// empty seen set, fetch the same message, and emit two notifications.
	seenMu    sync.Mutex
	seenLocks map[string]*sync.Mutex
}

func newNotifier(depsMap func() map[credential.CredentialSetID]Deps, state store.Store, memoryDir string) *notifier {
	return &notifier{
		depsMap:   depsMap,
		state:     state,
		memoryDir: memoryDir,
		run:       runGWS,
		cancels:   make(map[notification.SubscriptionID]context.CancelFunc),
		seenLocks: make(map[string]*sync.Mutex),
	}
}

// cursorKey returns the store key for a subscription's history cursor.
// The cursor IS per-subscription because each subscription may have been
// seeded at a different point in time.
func cursorKey(id notification.SubscriptionID) string {
	return "gmail_cursor/" + string(id)
}

// seenKey returns the store key for the rolling set of recently-notified
// message IDs for a credential set. Keyed by credential set (not subscription)
// so multiple subscriptions watching the same Gmail account share dedupe
// state — preventing parallel pollers from each emitting on the same message.
func seenKey(credSetID string) string {
	return "gmail_seen/" + credSetID
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

// seenLock returns the mutex guarding the seen set for a credential set,
// creating it on first use.
func (n *notifier) seenLock(credSetID string) *sync.Mutex {
	n.seenMu.Lock()
	defer n.seenMu.Unlock()
	m, ok := n.seenLocks[credSetID]
	if !ok {
		m = &sync.Mutex{}
		n.seenLocks[credSetID] = m
	}
	return m
}

// loadSeen returns the ordered list of recently-notified message IDs for the
// credential set (oldest first). Returns an empty slice on first use or on
// decode failure — a missing/corrupt seen set should never block notifications.
func (n *notifier) loadSeen(ctx context.Context, credSetID string) []string {
	data, err := n.state.Get(ctx, seenKey(credSetID))
	if err != nil || len(data) == 0 {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		slog.Warn("gmail notifier: failed to decode seen set, starting fresh",
			"credential_set", credSetID, "error", err)
		return nil
	}
	return ids
}

func (n *notifier) saveSeen(ctx context.Context, credSetID string, ids []string) {
	data, err := json.Marshal(ids)
	if err != nil {
		slog.Error("gmail notifier: failed to encode seen set", "credential_set", credSetID, "error", err)
		return
	}
	if err := n.state.Set(ctx, seenKey(credSetID), data); err != nil {
		slog.Error("gmail notifier: failed to persist seen set", "credential_set", credSetID, "error", err)
	}
}

// reserveSeen durably records messageID in the credential set's seen set so it
// is never re-notified — including across a restart between this call and the
// next poll. Persisting per message (rather than once per batch) keeps the
// at-least-once re-delivery window to a single in-flight message. Serialized on
// the per-credential seen lock so it composes safely with sibling pollers.
func (n *notifier) reserveSeen(ctx context.Context, credSetID, messageID string) {
	lock := n.seenLock(credSetID)
	lock.Lock()
	defer lock.Unlock()
	n.saveSeen(ctx, credSetID, appendCapped(n.loadSeen(ctx, credSetID), []string{messageID}, maxSeenMessageIDs))
}

func (n *notifier) NotificationTypes() []notification.NotificationType {
	return []notification.NotificationType{
		{
			Name:        TypeNewEmail,
			Description: "Watch for new emails using Gmail's history API. Polls every 2 minutes for changes since the last check — only new arrivals trigger a notification, not existing unread mail. Delivery is deduplicated and idempotent across restarts, and messages that no longer exist (deleted before they could be read) are skipped, so you are notified exactly once per real, still-present email.",
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

	// The seen set is intentionally NOT deleted here: it's keyed by credential
	// set and may still be in use by other subscriptions watching the same
	// account. Stale entries are bounded by maxSeenMessageIDs.
	n.deleteCursor(context.Background(), id)
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
		// Hold the cursor so the next poll retries the same window — no skip.
		slog.Error("gmail notifier: history fetch failed", "subscription", id, "error", err)
		return cursor
	}

	if len(newMessageIDs) == 0 {
		// Nothing new: safe to advance the cursor past this empty window.
		return n.advanceCursor(ctx, id, cursor, newHistoryID)
	}

	// Dedupe against the rolling seen set (shared per credential set) so
	// overlapping windows and sibling subscriptions don't re-notify. We only
	// READ here and reserve AFTER a successful emit below — reserving before
	// emit would let a fetch failure mark a message seen and silently drop it.
	lock := n.seenLock(credSetID)
	lock.Lock()
	seen := n.loadSeen(ctx, credSetID)
	freshMessageIDs, duplicates := filterSeen(newMessageIDs, seen)
	lock.Unlock()

	if duplicates > 0 {
		slog.Debug("gmail notifier: suppressed duplicate messages",
			"subscription", id, "credential_set", credSetID,
			"duplicates", duplicates, "fresh", len(freshMessageIDs))
	}

	if len(freshMessageIDs) == 0 {
		return n.advanceCursor(ctx, id, cursor, newHistoryID)
	}

	slog.Debug("gmail notifier: poll processing new messages",
		"subscription", id, "new_messages", len(freshMessageIDs), "cursor", cursor)

	deps, err := resolveDeps(n.depsMap(), credSetID)
	if err != nil {
		// Can't fetch bodies right now — hold the cursor and retry next poll.
		slog.Error("gmail notifier: resolve deps for message fetch", "subscription", id, "error", err)
		return cursor
	}

	// Emit one notification per email (never bundled) so each becomes its own
	// queue entry and the agent actions them independently. Each message is
	// reserved in the seen set the instant it's handled — emitted OR confirmed
	// gone — and persisted immediately, not batched at the end of the poll. This
	// is the dedupe guarantee: because the history cursor is inclusive, a restart
	// mid-poll re-reads this same window, so anything not yet reserved would
	// re-notify. Per-message reservation bounds that re-delivery to the single
	// in-flight message rather than the whole batch.
	emitFailed := false
	for _, messageID := range freshMessageIDs {
		text, outcome := n.buildNotification(ctx, deps, messageID)

		if outcome == fetchGone {
			// Message no longer exists — don't notify, but reserve the ID so the
			// next inclusive re-read of this window never surfaces it again.
			n.reserveSeen(ctx, credSetID, messageID)
			continue
		}

		if emitErr := emitter.Emit(ctx, notification.Notification{
			SubscriptionID: id,
			Text:           text,
		}); emitErr != nil {
			// Hold the cursor and stop: this message and the remainder are retried
			// next poll. It is deliberately NOT reserved, so it re-notifies
			// (at-least-once — better a rare duplicate than a dropped email).
			slog.Error("gmail notifier: emit failed", "subscription", id, "message_id", messageID, "error", emitErr)
			emitFailed = true
			break
		}

		// Reserve AFTER a successful emit — never before. Reserving before emit
		// would let an emit/crash failure mark a message seen and silently drop it.
		n.reserveSeen(ctx, credSetID, messageID)
	}

	if emitFailed {
		return cursor
	}

	return n.advanceCursor(ctx, id, cursor, newHistoryID)
}

// advanceCursor persists and returns newHistoryID as the cursor when it is
// non-empty; otherwise it keeps (holds) the current cursor. An empty
// newHistoryID means either no history change or paginate-cap hold — in both
// cases the next poll should re-read from the same point.
func (n *notifier) advanceCursor(ctx context.Context, id notification.SubscriptionID, cursor, newHistoryID string) string {
	if newHistoryID == "" {
		return cursor
	}
	n.saveCursor(ctx, id, newHistoryID)
	return newHistoryID
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

	output, err := n.run(ctx, deps, gws.Command{
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

// fetchHistory returns every message ID added since startHistoryID, following
// nextPageToken across all pages so a burst larger than one page is never
// dropped, and the mailbox's current historyId to use as the next cursor.
func (n *notifier) fetchHistory(ctx context.Context, credSetID, startHistoryID string) ([]string, string, error) {
	depsMap := n.depsMap()
	deps, err := resolveDeps(depsMap, credSetID)
	if err != nil {
		return nil, "", fmt.Errorf("resolve credential set %s: %w", credSetID, err)
	}

	// Deduplicate message IDs — the same message can appear across multiple
	// history records and pages.
	seen := make(map[string]bool)
	var messageIDs []string
	var latestHistoryID string
	pageToken := ""

	for page := 0; page < maxHistoryPages; page++ {
		params := map[string]any{
			"userId":         "me",
			"startHistoryId": startHistoryID,
			"historyTypes":   "messageAdded",
			"maxResults":     historyPageSize,
		}
		if pageToken != "" {
			params["pageToken"] = pageToken
		}

		output, err := n.run(ctx, deps, gws.Command{
			Args:   []string{"gmail", "users", "history", "list"},
			Params: params,
		})
		if err != nil {
			return nil, "", fmt.Errorf("list history (page %d): %w", page, err)
		}

		var rsp historyListResponse
		if err := json.Unmarshal(output, &rsp); err != nil {
			return nil, "", fmt.Errorf("parse history response: %w", err)
		}

		for _, record := range rsp.History {
			for _, added := range record.MessagesAdded {
				msgID := added.Message.ID
				if !seen[msgID] {
					seen[msgID] = true
					messageIDs = append(messageIDs, msgID)
				}
			}
		}

		if rsp.NextPageToken == "" {
			// Fully drained: this page's historyId is the safe next cursor.
			latestHistoryID = rsp.HistoryID
			break
		}
		pageToken = rsp.NextPageToken

		if page == maxHistoryPages-1 {
			// Cap hit with pages still remaining. Leave latestHistoryID empty so
			// the caller HOLDS the cursor — the messages we did collect are emitted
			// (and deduped via the seen set), and the next poll re-pages from the
			// same startHistoryId to reach the rest. Nothing is skipped.
			slog.Warn("gmail notifier: history pagination hit page cap, holding cursor for next poll",
				"credential_set", credSetID, "collected", len(messageIDs))
		}
	}

	return messageIDs, latestHistoryID, nil
}

type historyListResponse struct {
	History       []historyRecord `json:"history"`
	HistoryID     string          `json:"historyId"`
	NextPageToken string          `json:"nextPageToken"`
}

type historyRecord struct {
	MessagesAdded []messageAddedEvent `json:"messagesAdded"`
}

type messageAddedEvent struct {
	Message struct {
		ID string `json:"id"`
	} `json:"message"`
}
