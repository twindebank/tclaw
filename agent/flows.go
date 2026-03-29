package agent

import (
	"tclaw/channel"
	"tclaw/claudecli"
)

// FlowKind identifies which interactive flow is active on a channel.
type FlowKind int

const (
	FlowNone         FlowKind = iota
	FlowAuth                  // auth choosing, API key entry, OAuth, deploy confirm
	FlowReset                 // reset menu, confirmation
	FlowToolApproval          // tool denial → approve/deny
)

// ChannelFlow tracks the active interactive flow for a single channel.
// At most one flow is active per channel at any time.
type ChannelFlow struct {
	Kind         FlowKind
	Auth         *pendingAuth
	Reset        *pendingReset
	ToolApproval *pendingToolApproval
}

// FlowManager tracks per-channel interactive flows. Replaces the three
// separate maps (authFlows, resetFlows, toolApprovals) in RunWithMessages.
//
// At most one flow is active per channel. Starting a new flow on a channel
// cancels any existing flow on that channel.
type FlowManager struct {
	flows       map[channel.ChannelID]*ChannelFlow
	OAuthNotify chan channel.ChannelID
}

// NewFlowManager creates a FlowManager ready for use.
func NewFlowManager() *FlowManager {
	return &FlowManager{
		flows:       make(map[channel.ChannelID]*ChannelFlow),
		OAuthNotify: make(chan channel.ChannelID, 4),
	}
}

// Active returns the current flow for a channel, or nil if none.
func (fm *FlowManager) Active(chID channel.ChannelID) *ChannelFlow {
	return fm.flows[chID]
}

// HasFlow reports whether a flow of the given kind is active on this channel.
func (fm *FlowManager) HasFlow(chID channel.ChannelID, kind FlowKind) bool {
	f := fm.flows[chID]
	return f != nil && f.Kind == kind
}

// StartAuth begins an auth flow on a channel, cancelling any existing flow.
// Returns the new pendingAuth for the caller to configure.
func (fm *FlowManager) StartAuth(chID channel.ChannelID, originalMsg channel.TaggedMessage) *pendingAuth {
	fm.Cancel(chID)
	auth := &pendingAuth{
		state:       authChoosing,
		originalMsg: originalMsg,
	}
	fm.flows[chID] = &ChannelFlow{
		Kind: FlowAuth,
		Auth: auth,
	}
	return auth
}

// StartReset begins a reset flow on a channel, cancelling any existing flow.
func (fm *FlowManager) StartReset(chID channel.ChannelID) *pendingReset {
	fm.Cancel(chID)
	reset := &pendingReset{state: resetChoosing}
	fm.flows[chID] = &ChannelFlow{
		Kind:  FlowReset,
		Reset: reset,
	}
	return reset
}

// StartToolApproval begins a tool approval flow on a channel.
func (fm *FlowManager) StartToolApproval(chID channel.ChannelID, originalMsg channel.TaggedMessage, deniedTools []string, sessionID string) {
	fm.Cancel(chID)
	fm.flows[chID] = &ChannelFlow{
		Kind: FlowToolApproval,
		ToolApproval: &pendingToolApproval{
			originalMsg: originalMsg,
			deniedTools: deniedTools,
			sessionID:   sessionID,
		},
	}
}

// Cancel cancels whatever flow is active on this channel, cleaning up resources.
func (fm *FlowManager) Cancel(chID channel.ChannelID) {
	f := fm.flows[chID]
	if f == nil {
		return
	}
	if f.Auth != nil {
		f.Auth.cleanup()
	}
	delete(fm.flows, chID)
}

// Complete removes the flow for this channel (successful completion).
func (fm *FlowManager) Complete(chID channel.ChannelID) {
	delete(fm.flows, chID)
}

// FlowResult encodes the outcome of handling a message within a flow.
type FlowResult struct {
	// Handled is true if the message was consumed by the flow.
	Handled bool

	// RetryMessages are messages to prepend to the queue (e.g. original message
	// after successful auth).
	RetryMessages []channel.TaggedMessage

	// RestoreFunc reverts temporary permission changes after a turn completes.
	// Non-nil only for tool approval flows.
	RestoreFunc func()

	// FallThroughMsg is set when the flow wants the message to be processed
	// normally by handle() (e.g. tool approval "approve" → retry original msg).
	// When set, Handled is false and the caller should use this message.
	FallThroughMsg *channel.TaggedMessage

	// RestartAgent is non-nil when the flow requires an agent restart
	// (e.g. ResetProject or ResetAll).
	RestartAgent error
}

// buildApprovalOverride creates a temporary tool permission expansion for
// approved tools. Returns the modified message, the override to apply, and
// a restore function to revert after the turn.
func buildApprovalOverride(
	opts Options,
	approval *pendingToolApproval,
	chID channel.ChannelID,
) (msg channel.TaggedMessage, override ChannelToolPermissions, restore func()) {
	extraTools := make([]claudecli.Tool, len(approval.deniedTools))
	for i, t := range approval.deniedTools {
		extraTools[i] = claudecli.Tool(t)
	}

	originalOverride, hadOverride := opts.ChannelToolOverrides[chID]
	var base []claudecli.Tool
	if hadOverride {
		base = originalOverride.AllowedTools
	} else {
		base = opts.AllowedTools
	}

	expanded := make([]claudecli.Tool, 0, len(base)+len(extraTools))
	expanded = append(expanded, base...)
	expanded = append(expanded, extraTools...)

	newOverride := originalOverride
	newOverride.AllowedTools = expanded

	restore = func() {
		if hadOverride {
			opts.ChannelToolOverrides[chID] = originalOverride
		} else {
			delete(opts.ChannelToolOverrides, chID)
		}
	}

	return approval.originalMsg, newOverride, restore
}
