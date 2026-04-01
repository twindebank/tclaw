package reconciler

import (
	"context"
	"fmt"
	"log/slog"

	"tclaw/channel"
	"tclaw/config"
)

// ChannelStatus describes the reconciliation state of a channel.
type ChannelStatus string

const (
	// ChannelReady means the channel has everything it needs to run.
	ChannelReady ChannelStatus = "ready"

	// ChannelNeedsSetup means the channel is in config but the provisioner
	// reports it's not ready and can't auto-provision. The agent should guide
	// the user through manual setup.
	ChannelNeedsSetup ChannelStatus = "needs_setup"
)

// ReconciledChannel is the result of reconciling a single config channel.
type ReconciledChannel struct {
	Config       config.Channel
	RuntimeState *channel.RuntimeState
	Status       ChannelStatus

	// ProvisionErr is set when auto-provisioning failed. Reconcile() continues
	// past provisioning errors (marks channel as needs_setup), but ReconcileOne()
	// surfaces this error so tool calls get immediate feedback.
	ProvisionErr error
}

// ReconcileParams holds dependencies for reconciliation.
type ReconcileParams struct {
	Channels     []config.Channel
	SecretStore  interface{} // unused now but kept for ProvisionResult token storage
	RuntimeState *channel.RuntimeStateStore
	Provisioners map[channel.ChannelType]channel.EphemeralProvisioner
}

// Reconcile compares desired state (config channels) against actual state
// and converges them. For each channel:
//   - No provisioner for this type: ready (nothing to provision)
//   - Provisioner says IsReady: ready
//   - Not ready, can auto-provision: provision, then ready
//   - Not ready, can't auto-provision: needs_setup
func Reconcile(ctx context.Context, params ReconcileParams) ([]ReconciledChannel, error) {
	var results []ReconciledChannel

	for _, ch := range params.Channels {
		rs, err := params.RuntimeState.Get(ctx, ch.Name)
		if err != nil {
			return nil, fmt.Errorf("read runtime state for %q: %w", ch.Name, err)
		}

		provisioner, hasProvisioner := params.Provisioners[ch.Type]
		if !hasProvisioner {
			// No provisioner — nothing to provision (e.g. socket, stdio).
			results = append(results, ReconciledChannel{
				Config:       ch,
				RuntimeState: rs,
				Status:       ChannelReady,
			})
			continue
		}

		if provisioner.IsReady(ctx, ch.Name) {
			results = append(results, ReconciledChannel{
				Config:       ch,
				RuntimeState: rs,
				Status:       ChannelReady,
			})
			continue
		}

		if !provisioner.CanAutoProvision() {
			slog.Info("channel needs manual setup",
				"channel", ch.Name, "type", ch.Type)
			results = append(results, ReconciledChannel{
				Config:       ch,
				RuntimeState: rs,
				Status:       ChannelNeedsSetup,
			})
			continue
		}

		// Auto-provision.
		slog.Info("auto-provisioning channel", "channel", ch.Name, "type", ch.Type)
		provResult, provErr := provisioner.Provision(ctx, channel.ProvisionParams{
			Name:    ch.Name,
			Purpose: ch.Description,
		})
		if provErr != nil {
			slog.Error("auto-provisioning failed, marking as needs_setup",
				"channel", ch.Name, "err", provErr)
			results = append(results, ReconciledChannel{
				Config:       ch,
				RuntimeState: rs,
				Status:       ChannelNeedsSetup,
				ProvisionErr: provErr,
			})
			continue
		}

		// Store runtime state (platform state, teardown state).
		if err := params.RuntimeState.Update(ctx, ch.Name, func(state *channel.RuntimeState) {
			state.PlatformState = provResult.PlatformState
			state.TeardownState = provResult.TeardownState
		}); err != nil {
			return nil, fmt.Errorf("store runtime state for %q: %w", ch.Name, err)
		}

		rs, err = params.RuntimeState.Get(ctx, ch.Name)
		if err != nil {
			return nil, fmt.Errorf("re-read runtime state for %q: %w", ch.Name, err)
		}

		results = append(results, ReconciledChannel{
			Config:       ch,
			RuntimeState: rs,
			Status:       ChannelReady,
		})
	}

	return results, nil
}

// ReconcileOne reconciles a single channel. Used by channel tools after config
// mutations to give synchronous feedback to the agent. Unlike Reconcile(),
// this surfaces provisioning errors so the tool call fails visibly.
func ReconcileOne(ctx context.Context, ch config.Channel, params ReconcileParams) (*ReconciledChannel, error) {
	results, err := Reconcile(ctx, ReconcileParams{
		Channels:     []config.Channel{ch},
		RuntimeState: params.RuntimeState,
		Provisioners: params.Provisioners,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("reconciliation produced no result for channel %q", ch.Name)
	}
	rc := &results[0]
	if rc.ProvisionErr != nil {
		return nil, rc.ProvisionErr
	}
	return rc, nil
}
