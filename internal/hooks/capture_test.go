package hooks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/memorylayout"
)

func TestLessonCapture(t *testing.T) {
	t.Run("classifies what the user sends", func(t *testing.T) {
		tests := []struct {
			name   string
			prompt string
			queued bool
		}{
			{"pushback on work already done", "no, that's wrong — you keep doing this", true},
			{"a verdict on the work", "this is utter rubbish", true},
			{"asking why something was done", "why did you delete the retry?", true},
			{"a complaint about readability", "these comments are impossible to follow", true},
			{"a bare constraint on its own", "don't use a mock there", true},
			{"a task brief carrying a constraint", "add a retry to the uploader, and don't touch the tests", false},
			{"an ordinary request", "can you check whether the deploy finished?", false},
			{"a constraint at the end of a long brief", longBriefEndingInAConstraint(), false},
			{"a prompt the harness wrote", "<system-reminder> do not repeat work that is already complete", false},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				h := setup(t)

				code, out := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
					"prompt":     test.prompt,
					"session_id": "sess-8f21c4",
				})

				require.Equal(t, 0, code, "capture must never refuse a turn: %s", out)
				rows := readInbox(t, h.ConfigDir)
				if !test.queued {
					require.Empty(t, rows, "should not have been filed")
					return
				}
				require.Len(t, rows, 1)
				require.Equal(t, "user_correction", rows[0].Kind)
				require.Equal(t, test.prompt, rows[0].Detail)
				require.Equal(t, "sess-8f21c4", rows[0].SessionID)
				require.Equal(t, "admin", rows[0].Channel)
				require.NotEmpty(t, rows[0].Trigger, "the row must say what matched")
			})
		}
	})

	t.Run("files anything marked !log and says what the marker means", func(t *testing.T) {
		h := setup(t)

		code, out := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"prompt":     "!log prefer the shorter form of that heading",
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 0, code)
		require.Contains(t, out, "Filed for the retro")
		require.Contains(t, out, "do not action, debate or write this up")

		rows := readInbox(t, h.ConfigDir)
		require.Len(t, rows, 1)
		require.Equal(t, "!log", rows[0].Trigger)
		require.Equal(t, "user_correction", rows[0].Kind)
	})

	t.Run("ignores a prompt that only writes about the marker", func(t *testing.T) {
		h := setup(t)

		// Editing the documentation that describes the marker must not file a
		// correction that never happened.
		code, _ := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"prompt":     "update the docs to explain that `!log` files something for the retro",
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 0, code)
		require.Empty(t, readInbox(t, h.ConfigDir))
	})

	t.Run("ignores a long paste that happens to contain a trigger word", func(t *testing.T) {
		h := setup(t)
		paste := "FAIL: expected 3, got 4 — wrong\n" + strings.Repeat("stack frame line\n", 200)
		require.Greater(t, len(paste), 2000, "the fixture must be long enough to be a paste")

		code, _ := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"prompt":     paste,
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 0, code)
		require.Empty(t, readInbox(t, h.ConfigDir))
	})

	t.Run("keeps a row on one line", func(t *testing.T) {
		h := setup(t)

		code, _ := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"prompt":     "no, that's wrong.\n\nthe second paragraph\tand a tab",
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 0, code)
		raw, err := os.ReadFile(memorylayout.InboxPath(h.ConfigDir))
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(raw), "\n"), "one row is one line")
	})

	t.Run("mentions the queue once it is worth draining, then not again until it grows", func(t *testing.T) {
		h := setup(t)

		// Two corrections is under the threshold, so nothing is said yet.
		for i := range 2 {
			_, out := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
				"prompt":     fmt.Sprintf("no, that's wrong (%d)", i),
				"session_id": "sess-8f21c4",
			})
			require.NotContains(t, out, "waiting in the retro queue")
		}

		_, out := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"prompt":     "no, that's wrong again",
			"session_id": "sess-8f21c4",
		})
		require.Contains(t, out, "3 corrections are waiting in the retro queue")
		require.Contains(t, out, "retro skill")

		// The next two land under the step, so the nudge holds its tongue.
		for i := range 2 {
			_, out := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
				"prompt":     fmt.Sprintf("no, that's still wrong (%d)", i),
				"session_id": "sess-8f21c4",
			})
			require.NotContains(t, out, "waiting in the retro queue")
		}

		_, out = runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"prompt":     "no, wrong once more",
			"session_id": "sess-8f21c4",
		})
		require.Contains(t, out, "6 corrections are waiting in the retro queue",
			"a repeat must say how far the queue has grown")
	})

	t.Run("passes when it cannot tell where the config dir is", func(t *testing.T) {
		h := setup(t)

		code, out := runHook(t, h.Bin, "lesson-capture", []string{}, map[string]any{
			"prompt":     "no, that's wrong",
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 0, code, "capture must fail open: %s", out)
		require.Empty(t, readInbox(t, h.ConfigDir))
	})

	t.Run("passes on an empty prompt", func(t *testing.T) {
		h := setup(t)

		code, out := runHook(t, h.Bin, "lesson-capture", hookEnv(h, "admin"), map[string]any{
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 0, code)
		require.Empty(t, out)
		require.Empty(t, readInbox(t, h.ConfigDir))
	})
}

func TestBlockQueuesTheRefusal(t *testing.T) {
	t.Run("a refused rulebook write reaches the retro queue", func(t *testing.T) {
		h := setup(t)

		code, _ := runHook(t, h.Bin, "rules-gate", hookEnv(h, "admin"), map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "rules", "invoices.md")},
			"session_id": "sess-8f21c4",
		})

		require.Equal(t, 2, code)
		rows := readInbox(t, h.ConfigDir)
		require.Len(t, rows, 1, "being stopped is what a retro exists to read")
		require.Equal(t, "guard_block", rows[0].Kind)
		require.Equal(t, "rules-gate", rows[0].Trigger)
		require.Contains(t, rows[0].Detail, "invoices.md")
	})
}

// --- helpers ---

// longBriefEndingInAConstraint is a brief long enough to read as new work, with
// a ground rule at the end.
func longBriefEndingInAConstraint() string {
	// Deliberately one unbroken line: a dictated or pasted brief arrives that
	// way, so nothing in the filter may key on whitespace.
	return strings.Repeat("Pick up the uploader work and take it through to a merged PR. ", 15) +
		"Do NOT make any code changes to the tests."
}
