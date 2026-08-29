package hooks

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tclaw/internal/memorylayout"
)

// FeedbackKind says why an event was queued. Only kinds a retro acts on are
// written, so the queue stays short enough to read line by line.
type FeedbackKind string

const (
	// KindUserCorrection is the user pushing back on work already done.
	KindUserCorrection FeedbackKind = "user_correction"

	// KindGuardBlock is a hook refusing a tool call. Being stopped is hard
	// evidence a rule did not hold on its own, which is what a retro reads.
	KindGuardBlock FeedbackKind = "guard_block"
)

// feedbackEvent is one row of the queue.
type feedbackEvent struct {
	Timestamp string       `json:"timestamp"`
	SessionID string       `json:"session_id"`
	Channel   string       `json:"channel"`
	Kind      FeedbackKind `json:"kind"`
	Trigger   string       `json:"trigger"`
	Detail    string       `json:"detail"`
}

// detailCap is the longest detail a row carries. A long paste is a paste however
// it ends, and the queue is read line by line at retro time.
const detailCap = 2000

// feedbackEntry is what a caller knows about an event. queueFeedback stamps the
// time and the channel, which come from the environment rather than the caller.
type feedbackEntry struct {
	SessionID string
	Kind      FeedbackKind
	Trigger   string
	Detail    string
}

// queueFeedback appends one event to the retro queue. A row that cannot be
// written is logged and dropped: a lost row beats a hook that breaks the turn.
func queueFeedback(entry feedbackEntry) {
	configDir := os.Getenv(memorylayout.EnvConfigDir)
	if configDir == "" {
		slog.Warn("no config dir, dropping feedback row", "kind", entry.Kind, "trigger", entry.Trigger)
		return
	}

	session := entry.SessionID
	if session == "" {
		session = "unknown"
	}
	encoded, err := json.Marshal(feedbackEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		SessionID: session,
		Channel:   os.Getenv(memorylayout.EnvChannel),
		Kind:      entry.Kind,
		Trigger:   flatten(entry.Trigger),
		Detail:    flatten(capDetail(entry.Detail)),
	})
	if err != nil {
		slog.Error("failed to encode feedback row", "kind", entry.Kind, "err", err)
		return
	}

	dir := memorylayout.FeedbackDir(configDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error("failed to create feedback dir", "dir", dir, "err", err)
		return
	}
	inbox := memorylayout.InboxPath(configDir)
	f, err := os.OpenFile(inbox, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("failed to open feedback queue", "path", inbox, "err", err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close feedback queue", "path", inbox, "err", err)
		}
	}()
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		slog.Error("failed to append feedback row", "path", inbox, "err", err)
	}
}

// capDetail trims a detail to the cap, saying so rather than cutting silently.
func capDetail(detail string) string {
	if len(detail) <= detailCap {
		return detail
	}
	// Back off to a character boundary, so the last thing in the row is not a
	// half-written character the reader has to puzzle over.
	cut := detailCap
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut] + " … (truncated)"
}

// flatten puts a value on one line so a row stays one line of JSON to scan.
func flatten(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// queuedCorrections counts the rows a retro would act on: the live inbox, plus
// anything stranded in processing.jsonl by a retro that snapshotted the inbox
// and then never archived it. Without the second file, that snapshot moving
// the rows out of inbox.jsonl reads as the queue draining rather than as a
// retro still owing an answer, and the nudge goes quiet on rows nobody judged.
func queuedCorrections(configDir string) int {
	return countCorrections(memorylayout.InboxPath(configDir)) +
		countCorrections(memorylayout.ProcessingPath(configDir))
}

// countCorrections counts actionable rows in one queue file.
func countCorrections(path string) int {
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// Nothing captured yet, which is a count of zero rather than a problem.
		return 0
	case err != nil:
		slog.Error("failed to read feedback queue", "path", path, "err", err)
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		var event feedbackEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Timestamp != "" && (event.Kind == KindUserCorrection || event.Kind == KindGuardBlock) {
			n++
		}
	}
	return n
}

// retroThreshold is how many queued rows make a retro worth mentioning.
const retroThreshold = 3

// retroNudgeStep is how much the queue must grow before the nudge speaks again.
// Keyed on the queue rather than the session: the same session can fill it twice
// over, and one mention per session would go quiet for the rest of a long one.
const retroNudgeStep = 3

// nudgeMarkerName records the queue size the nudge last spoke at.
const nudgeMarkerName = ".retro-nudged-at"

// retroNudge returns what to tell the agent about the waiting queue, or "" when
// it is too small or has not grown enough since the last mention.
func retroNudge(configDir string) string {
	waiting := queuedCorrections(configDir)
	marker := filepath.Join(memorylayout.FeedbackDir(configDir), nudgeMarkerName)
	last := lastNudgedAt(marker)
	if waiting < last {
		// The queue only shrinks when a retro has judged and archived it. Reset
		// the baseline to zero — and persist that immediately — rather than
		// waiting for growth to be measured against it: a fresh batch that
		// happens to regrow to exactly the old high-water mark would otherwise
		// look like it never grew at all, and the nudge would stay silent on
		// rows nobody has judged.
		last = 0
		if err := os.WriteFile(marker, []byte(strconv.Itoa(last)), 0o600); err != nil {
			slog.Warn("failed to record retro queue drain", "path", marker, "err", err)
		}
	}
	if waiting < retroThreshold || waiting < last+retroNudgeStep {
		return ""
	}
	if err := os.WriteFile(marker, []byte(strconv.Itoa(waiting)), 0o600); err != nil {
		// Say it anyway. Repeating a nudge is a smaller failure than never
		// giving it, and the marker is only there to keep it occasional.
		slog.Warn("failed to record retro nudge", "path", marker, "err", err)
	}
	return fmt.Sprintf("📝 %d corrections are waiting in the retro queue (%s). "+
		"When there is a good moment, use your retro skill to turn them into rules or fixes — "+
		"it should judge them in a fresh session rather than this one.", waiting, memorylayout.FeedbackDir(configDir))
}

// lastNudgedAt reads the queue size the nudge last spoke at. Anything unreadable
// counts as zero, so a lost marker means one extra nudge rather than silence.
func lastNudgedAt(marker string) int {
	raw, err := os.ReadFile(marker)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return n
}
