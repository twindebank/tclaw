package channel

import (
	"context"

	"tclaw/role"
)

// MessageID identifies a sent message so it can be edited later.
// The concrete value is transport-specific (e.g. telegram message ID,
// slack timestamp, or an internal sequence number for sockets).
type MessageID string

// ChannelID uniquely identifies a channel. The value comes from the
// transport itself (e.g. telegram bot ID, slack team ID, socket path).
type ChannelID string

// ChannelType identifies the transport kind so the agent can adapt
// its formatting, message length, etc.
type ChannelType string

// Markup describes which rich-text format a channel's Send/Edit methods accept.
type Markup string

const (
	// MarkupMarkdown is standard markdown (socket, stdio).
	MarkupMarkdown Markup = "markdown"
	// MarkupHTML is Telegram-style HTML (<b>, <code>, etc.).
	MarkupHTML Markup = "html"
)

const (
	TypeSocket   ChannelType = "socket"
	TypeStdio    ChannelType = "stdio"
	TypeTelegram ChannelType = "telegram"
	TypeSlack    ChannelType = "slack"
)

// Source indicates where a channel's configuration came from.
type Source string

const (
	SourceStatic  Source = "static"  // from config file, not editable via tools
	SourceDynamic Source = "dynamic" // user-created via channel management tools
)

// Info describes a channel's identity and transport type.
type Info struct {
	ID          ChannelID
	Type        ChannelType
	Name        string // human-readable label
	Description string // explains the channel's purpose (e.g. "Desktop workstation", "Phone")
	Source      Source // where this channel's config came from

	// Role is a named preset of tool permissions. Mutually exclusive with
	// AllowedTools — set one or the other.
	Role role.Role

	// AllowedTools overrides user-level tool permissions for this channel.
	// Uses []string (not []claudecli.Tool) to avoid circular dependency.
	AllowedTools []string

	// DisallowedTools overrides user-level tool permissions for this channel.
	DisallowedTools []string

	// NotifyLifecycle sends a message to this channel on instance startup and shutdown.
	NotifyLifecycle bool
}

// MessageSource identifies where a message originated.
type MessageSource string

const (
	SourceUser     MessageSource = "user"     // typed by a human on the channel
	SourceSchedule MessageSource = "schedule" // fired by a cron schedule
	SourceChannel  MessageSource = "channel"  // sent from another channel via channel_send
)

// MessageSourceInfo carries attribution details for a message.
type MessageSourceInfo struct {
	Source MessageSource

	// FromChannel is the name of the source channel (set when Source == SourceChannel).
	FromChannel string

	// ScheduleName is the schedule's human-readable name (set when Source == SourceSchedule).
	ScheduleName string
}

// TaggedMessage pairs an incoming message with the channel it arrived on
// so the agent can route responses back to the correct transport.
type TaggedMessage struct {
	ChannelID ChannelID
	Text      string

	// SourceInfo describes where this message came from. Nil is treated as
	// SourceUser for backwards compatibility.
	SourceInfo *MessageSourceInfo
}

// StatusWrap holds the open and close tags for wrapping status content
// (thinking, tool use, tool results, stats). Channels that don't support
// collapsible content return empty strings.
type StatusWrap struct {
	Open  string // e.g. "<blockquote expandable>"
	Close string // e.g. "</blockquote>"
}

// Channel is the interface every transport must implement.
type Channel interface {
	// Info returns the channel's identity and transport type.
	Info() Info
	// Messages returns a channel of incoming user messages.
	Messages(ctx context.Context) <-chan string
	// Send delivers a chunk of response to the user and returns an
	// identifier that can be passed to Edit to update the message.
	Send(ctx context.Context, text string) (MessageID, error)
	// Edit replaces the content of a previously sent message.
	Edit(ctx context.Context, id MessageID, text string) error
	// Done signals the end of a response turn.
	Done(ctx context.Context) error
	// SplitStatusMessages reports whether the channel wants thinking/tool-use
	// status separated from the response text into distinct messages.
	SplitStatusMessages() bool
	// Markup returns the rich-text format the channel accepts.
	Markup() Markup
	// StatusWrap returns the open/close tags for wrapping all status
	// content (thinking, tool use, stats) in a collapsible block.
	// Returns empty strings for channels that don't support this.
	StatusWrap() StatusWrap
}
