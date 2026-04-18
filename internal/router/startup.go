package router

import (
	"context"

	"tclaw/internal/channel"
	"tclaw/internal/queue"
)

// StartupDecision captures whether the router should kick the agent off
// immediately on boot and, if so, whether it has a synthetic first message
// to hand the agent before it starts pulling from the queue itself.
type StartupDecision struct {
	// StartNow is true when there's enough to do right now to warrant
	// spinning up the agent. False means waitAndStart should block on its
	// live inbound stream and wait for a fresh user/schedule/notification
	// message to arrive.
	StartNow bool

	// FirstMessage is a synthetic message to process before Queue.Next()
	// takes over. It's set for resume-after-interrupt (the agent needs an
	// explicit "you were interrupted" turn so the model re-orients). It's
	// nil when persisted work alone is enough to warrant a start — in that
	// case Queue.Next() drains the queue on its own, no synthetic turn
	// required.
	FirstMessage *channel.TaggedMessage
}

// determineStartupSignal decides whether the router should start the agent
// immediately on boot.
//
// Three cases:
//
//  1. An interrupted-channel marker is set (agent was force-killed mid-turn
//     before the last turn completed): start now with a synthetic resume
//     message so the model re-orients.
//  2. The queue holds persisted work that survived a restart (e.g. a
//     scheduled fire whose turn was cut short by a deploy): start now with
//     no synthetic message — Queue.Next() will dequeue the persisted
//     messages on its first call.
//  3. Nothing to act on: return a zero decision so waitAndStart blocks on
//     its live inbound stream.
//
// The queue must already have had LoadPersisted called on it — without that
// the interrupted marker and persisted messages are both invisible.
func determineStartupSignal(ctx context.Context, q *queue.Queue, channels map[channel.ChannelID]channel.Channel) StartupDecision {
	if resume := checkAutoResume(ctx, q, channels); resume != nil {
		return StartupDecision{StartNow: true, FirstMessage: resume}
	}
	if q.Len() > 0 {
		return StartupDecision{StartNow: true}
	}
	return StartupDecision{}
}
