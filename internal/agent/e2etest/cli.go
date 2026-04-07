package e2etest

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/id"
)

// Turn represents a simulated CLI turn written as stream-json events.
// Use TextBlock/ToolBlock/ThinkingBlock to build Blocks, or use
// the Respond shortcut for simple text responses.
type Turn struct {
	// SessionID for the turn. Auto-generated if empty.
	SessionID string

	// Delay before writing events. Zero means immediate.
	Delay time.Duration

	// Blocks to emit. Each becomes a content_block_start + deltas + stop sequence.
	Blocks []Block

	// Error emits an error result instead of a success result.
	// Mutually exclusive with Blocks (error turns have no content).
	Error *TurnError
}

// Block is a single content block in a turn.
type Block struct {
	Type      claudecli.ContentBlockType
	Text      string          // text or thinking content
	ToolName  string          // tool_use only
	ToolInput json.RawMessage // tool_use only
}

// TurnError configures error responses.
type TurnError struct {
	AuthFailed  bool   // assistant event with error="authentication_failed"
	RateLimited bool   // result event with is_error=true, rate limit message
	Message     string // generic error message
}

// TextBlock creates a text content block.
func TextBlock(text string) Block {
	return Block{Type: claudecli.ContentText, Text: text}
}

// ToolBlock creates a tool_use content block.
func ToolBlock(name string, input json.RawMessage) Block {
	return Block{Type: claudecli.ContentToolUse, ToolName: name, ToolInput: input}
}

// ThinkingBlock creates a thinking content block.
func ThinkingBlock(text string) Block {
	return Block{Type: claudecli.ContentThinking, Text: text}
}

// CommandFunc returns an CommandFunc that writes this turn as stream-json.
func (t Turn) CommandFunc() CommandFunc {
	return func(ctx context.Context, args []string, env []string, dir string) (io.ReadCloser, func() error, error) {
		pr, pw := io.Pipe()

		go func() {
			defer pw.Close()
			enc := json.NewEncoder(pw)

			if t.Delay > 0 {
				select {
				case <-time.After(t.Delay):
				case <-ctx.Done():
					return
				}
			}

			sessionID := t.SessionID
			if sessionID == "" {
				sessionID = id.Generate("session")
			}

			// Auth failure is a special assistant event, not a normal turn.
			if t.Error != nil && t.Error.AuthFailed {
				enc.Encode(map[string]any{
					"type":  "assistant",
					"error": claudecli.AssistantErrorAuthFailed,
					"message": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "Not logged in"},
						},
					},
				})
				return
			}

			// System init event.
			enc.Encode(map[string]any{
				"type":       "system",
				"subtype":    "init",
				"session_id": sessionID,
			})

			// Content blocks.
			for _, block := range t.Blocks {
				switch block.Type {
				case claudecli.ContentText:
					enc.Encode(map[string]any{
						"type":          "content_block_start",
						"content_block": map[string]any{"type": "text", "text": ""},
					})
					enc.Encode(map[string]any{
						"type":  "content_block_delta",
						"delta": map[string]any{"type": "text_delta", "text": block.Text},
					})
					enc.Encode(map[string]any{"type": "content_block_stop"})

				case claudecli.ContentToolUse:
					input := block.ToolInput
					if input == nil {
						input = json.RawMessage(`{}`)
					}
					enc.Encode(map[string]any{
						"type": "content_block_start",
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    id.Generate("tool"),
							"name":  block.ToolName,
							"input": json.RawMessage(input),
						},
					})
					enc.Encode(map[string]any{"type": "content_block_stop"})

				case claudecli.ContentThinking:
					enc.Encode(map[string]any{
						"type":          "content_block_start",
						"content_block": map[string]any{"type": "thinking", "thinking": ""},
					})
					enc.Encode(map[string]any{
						"type":  "content_block_delta",
						"delta": map[string]any{"type": "thinking_delta", "thinking": block.Text},
					})
					enc.Encode(map[string]any{"type": "content_block_stop"})
				}
			}

			// Result event.
			result := map[string]any{
				"type":           "result",
				"session_id":     sessionID,
				"is_error":       false,
				"duration_ms":    100,
				"num_turns":      1,
				"total_cost_usd": 0.001,
			}

			if t.Error != nil {
				result["is_error"] = true
				if t.Error.RateLimited {
					result["result"] = "rate limit exceeded"
				} else if t.Error.Message != "" {
					result["result"] = t.Error.Message
				} else {
					result["result"] = "error"
				}
			}

			enc.Encode(result)
		}()

		return pr, func() error { return nil }, nil
	}
}

// Respond is a convenience that emits a single text response.
func Respond(text string) CommandFunc {
	return Turn{Blocks: []Block{TextBlock(text)}}.CommandFunc()
}

// Script maps a match function to a CommandFunc response.
type Script struct {
	Match   func(args []string) bool
	Respond CommandFunc
}

// Scripted dispatches to different CommandFuncs based on CLI args.
// Falls back to fallback if no script matches (nil fallback returns empty response).
func Scripted(scripts []Script, fallback CommandFunc) CommandFunc {
	if fallback == nil {
		fallback = Respond("")
	}
	return func(ctx context.Context, args []string, env []string, dir string) (io.ReadCloser, func() error, error) {
		for _, s := range scripts {
			if s.Match(args) {
				return s.Respond(ctx, args, env, dir)
			}
		}
		return fallback(ctx, args, env, dir)
	}
}

// MatchPrompt returns a match function that checks the --prompt arg for a substring.
func MatchPrompt(substr string) func([]string) bool {
	return func(args []string) bool {
		return strings.Contains(ExtractPrompt(args), substr)
	}
}
