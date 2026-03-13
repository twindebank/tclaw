package channel

import "context"

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

	// AllowedTools overrides user-level tool permissions for this channel.
	// Uses []string (not []claudecli.Tool) to avoid circular dependency.
	AllowedTools []string

	// DisallowedTools overrides user-level tool permissions for this channel.
	DisallowedTools []string
}

// TaggedMessage pairs an incoming message with the channel it arrived on
// so the agent can route responses back to the correct transport.
type TaggedMessage struct {
	ChannelID ChannelID
	Text      string
}

// ThinkingWrap holds the open and close tags for wrapping thinking content.
// Channels that don't support collapsible/spoiler content return empty strings.
type ThinkingWrap struct {
	Open  string // e.g. "<tg-spoiler>"
	Close string // e.g. "</tg-spoiler>"
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
	// ThinkingWrap returns the open/close tags for wrapping thinking
	// content in a collapsible or spoiler block. Returns empty strings
	// for channels that don't support this.
	ThinkingWrap() ThinkingWrap
}
