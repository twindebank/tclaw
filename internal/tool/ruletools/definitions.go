// Package ruletools exposes the rulebook pool: what rules exist, which channels
// load which, and the one route by which a rule may change.
//
// Rulebooks are the user's standing decisions, so the agent proposes and the user
// decides. rule_propose sends a prompt to the channel and the router writes the
// file only on the user's reply, outside the sandbox — the agent never writes one
// itself, and the rules-gate hook refuses a direct file write to the pool.
//
// Reading is deliberately unrestricted. Every rulebook can be read from every
// channel; a channel's index only decides which arrive in context without being
// asked for.
package ruletools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tclaw/internal/mcp"
	"tclaw/internal/memorylayout"
)

const (
	ToolList    = "rule_list"
	ToolPropose = "rule_propose"
)

// RuleWriteRequest is a proposed change to one rulebook, as shown to the user.
type RuleWriteRequest struct {
	// File is the rulebook's name within the pool, e.g. "automations.md".
	File string

	// Content is the full text the file will have. A whole-file write keeps the
	// user's decision and the resulting file identical: with a patch they would
	// be approving one thing and getting another.
	Content string

	// Reason is why the change is wanted, shown in the confirmation prompt.
	Reason string
}

// Deps are what the rule tools need from the router.
type Deps struct {
	MemoryDir string

	// ArmRuleWrite asks the user to confirm a rulebook change. Nil when no
	// channel can be asked, which makes proposing fail rather than silently
	// skipping the confirmation.
	ArmRuleWrite func(ctx context.Context, request RuleWriteRequest) error
}

func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(ruleListDef(), ruleListHandler(deps))
	handler.Register(ruleProposeDef(), ruleProposeHandler(deps))
}

// ToolNames lists this package's tools for the info tool.
func ToolNames() []string {
	return []string{ToolList, ToolPropose}
}

func ruleListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolList,
		Description: "List the rulebooks — the user's standing decisions about how work is done, one file per area.\n\n" +
			"Shows every rulebook, and for each one the channels that load it automatically and the channels " +
			"that only mention it. You can Read any of them at any time, including ones this channel does not " +
			"load: the channel index decides what arrives without being asked for, not what you may look at.\n\n" +
			"Use this when you want to know whether a rule already exists before acting on your own judgement.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func ruleListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		if deps.MemoryDir == "" {
			return nil, fmt.Errorf("no memory directory configured, so there are no rulebooks to list")
		}
		books, err := listRulebooks(deps.MemoryDir)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"rules_dir": memorylayout.RulesDir(deps.MemoryDir),
			"rulebooks": books,
		})
	}
}

func ruleProposeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolPropose,
		Description: "Propose a rulebook, or a change to one, and ask the user to approve it.\n\n" +
			"Rulebooks hold the user's standing decisions, so you may propose and they decide. This sends " +
			"them the full proposed text and returns status \"awaiting_confirmation\". Do NOT answer that " +
			"prompt yourself and do not send further messages about it — the file is written only on their " +
			"reply, and only they can give it.\n\n" +
			"Pass the COMPLETE text the file should have, not a patch: the user approves exactly what gets " +
			"written. Read the existing file first and include the parts you are keeping.\n\n" +
			"Writing to the rules directory with Write or Edit is refused, so this is the only route.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file": {
					"type": "string",
					"description": "Rulebook file name within the rules directory, e.g. 'automations.md'. A new name creates a new rulebook."
				},
				"content": {
					"type": "string",
					"description": "The complete text the file should have after the change, including everything being kept."
				},
				"reason": {
					"type": "string",
					"description": "Why the change is wanted, shown to the user so they are deciding on something specific."
				}
			},
			"required": ["file", "content", "reason"]
		}`),
	}
}

type ruleProposeArgs struct {
	File    string `json:"file"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

func ruleProposeHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a ruleProposeArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		name, err := rulebookName(a.File)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Content) == "" {
			return nil, fmt.Errorf("content is required — pass the complete text the file should have")
		}
		if strings.TrimSpace(a.Reason) == "" {
			return nil, fmt.Errorf("reason is required — the user sees it when deciding")
		}
		if deps.ArmRuleWrite == nil {
			return nil, fmt.Errorf("rule changes are unavailable — no way to ask the user for confirmation")
		}

		if err := deps.ArmRuleWrite(ctx, RuleWriteRequest{File: name, Content: a.Content, Reason: a.Reason}); err != nil {
			return nil, fmt.Errorf("ask for confirmation: %w", err)
		}
		return json.Marshal(map[string]any{
			"file":   name,
			"status": "awaiting_confirmation",
			"note":   "The user has been asked. Do not answer for them, and do not follow up — their reply writes the file.",
		})
	}
}

// rulebookName validates a proposed file name. The pool is flat and markdown-only,
// so a name carrying a path separator is either a mistake or an attempt to write
// somewhere else entirely.
func rulebookName(file string) (string, error) {
	name := strings.TrimSpace(file)
	if name == "" {
		return "", fmt.Errorf("file is required, e.g. 'automations.md'")
	}
	if name != filepath.Base(name) || strings.Contains(file, string(os.PathSeparator)) {
		return "", fmt.Errorf("file must be a plain name inside the rules directory, not a path")
	}
	if filepath.Ext(name) != ".md" {
		return "", fmt.Errorf("rulebooks are markdown: %q needs a .md suffix", name)
	}
	return name, nil
}

// rulebook is one file in the pool and where it is referenced.
type rulebook struct {
	File     string   `json:"file"`
	LoadedBy []string `json:"loaded_by"`
	ListedBy []string `json:"listed_by"`
}

// listRulebooks reads the pool and works out, for each rulebook, which channels
// import it and which only mention it.
func listRulebooks(memoryDir string) ([]rulebook, error) {
	rulesDir := memorylayout.RulesDir(memoryDir)
	entries, err := os.ReadDir(rulesDir)
	if os.IsNotExist(err) {
		return []rulebook{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read rules dir: %w", err)
	}

	indexes, err := filepath.Glob(filepath.Join(memoryDir, memorylayout.ChannelsDirName, "*", "CLAUDE.md"))
	if err != nil {
		return nil, fmt.Errorf("find channel indexes: %w", err)
	}
	type index struct {
		channel string
		text    string
	}
	var loaded []index
	for _, path := range indexes {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read channel index %s: %w", path, readErr)
		}
		loaded = append(loaded, index{channel: filepath.Base(filepath.Dir(path)), text: string(raw)})
	}

	var books []rulebook
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || entry.Name() == "README.md" {
			continue
		}
		book := rulebook{File: entry.Name(), LoadedBy: []string{}, ListedBy: []string{}}
		importLine := "@../../" + memorylayout.RulesDirName + "/" + entry.Name()
		for _, idx := range loaded {
			switch {
			case strings.Contains(idx.text, importLine):
				book.LoadedBy = append(book.LoadedBy, idx.channel)
			case strings.Contains(idx.text, entry.Name()):
				book.ListedBy = append(book.ListedBy, idx.channel)
			}
		}
		sort.Strings(book.LoadedBy)
		sort.Strings(book.ListedBy)
		books = append(books, book)
	}
	sort.Slice(books, func(i, j int) bool { return books[i].File < books[j].File })
	return books, nil
}
