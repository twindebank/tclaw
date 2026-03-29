package router

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/dev"
	"tclaw/onboarding"
	"tclaw/user"
)

// PromptParams holds everything needed to build the system prompt for one
// agent iteration.
type PromptParams struct {
	Channels   map[channel.ChannelID]channel.Channel
	Registry   *channel.Registry
	DevStore   *dev.Store
	UserDir    string
	UserID     user.ID
	BasePrompt string
	Onboarding *onboarding.Store
}

// IterationPromptResult holds the system prompt plus side-effect data
// computed during prompt building (add-dirs, dev session info).
type IterationPromptResult struct {
	SystemPrompt string
	AddDirs      []string
}

// BuildIterationPrompt constructs the system prompt for one agent iteration.
// Also returns the add-dirs list (worktree + repo dirs for sandbox mounting).
func BuildIterationPrompt(ctx context.Context, params PromptParams) IterationPromptResult {
	// Build channel info for the prompt.
	var chInfos []agent.ChannelInfo
	for _, ch := range params.Channels {
		info := ch.Info()
		chInfos = append(chInfos, agent.ChannelInfo{
			Name:        info.Name,
			Type:        string(info.Type),
			Description: info.Description,
			Source:      string(info.Source),
		})
	}

	// Populate outbound links and compute inbound links by inverting the graph.
	allLinks, _ := params.Registry.Links(ctx)
	for i, chInfo := range chInfos {
		if links, ok := allLinks[chInfo.Name]; ok {
			for _, link := range links {
				chInfos[i].OutboundLinks = append(chInfos[i].OutboundLinks, agent.ChannelLinkInfo{
					ChannelName: link.Target,
					Description: link.Description,
				})
			}
		}
	}
	for _, chInfo := range chInfos {
		for _, out := range chInfo.OutboundLinks {
			for j, target := range chInfos {
				if target.Name == out.ChannelName {
					chInfos[j].InboundLinks = append(chInfos[j].InboundLinks, agent.ChannelLinkInfo{
						ChannelName: chInfo.Name,
						Description: out.Description,
					})
				}
			}
		}
	}

	// Build dev session info and add-dirs.
	var devSessionInfos []agent.DevSessionInfo
	worktreesDir := filepath.Join(params.UserDir, "worktrees")
	if mkErr := os.MkdirAll(worktreesDir, 0o755); mkErr != nil {
		slog.Warn("failed to create worktrees dir", "err", mkErr, "user", params.UserID)
	}
	reposDir := filepath.Join(params.UserDir, "repos")
	if mkErr := os.MkdirAll(reposDir, 0o755); mkErr != nil {
		slog.Warn("failed to create repos dir", "err", mkErr, "user", params.UserID)
	}
	addDirs := []string{worktreesDir, reposDir}

	devSessions, devErr := params.DevStore.ListSessions(ctx)
	if devErr != nil {
		slog.Error("failed to list dev sessions", "err", devErr)
	}
	for _, sess := range devSessions {
		devSessionInfos = append(devSessionInfos, agent.DevSessionInfo{
			Branch:      sess.Branch,
			WorktreeDir: sess.WorktreeDir,
			Age:         time.Since(sess.CreatedAt).Truncate(time.Minute).String(),
			Stale:       time.Since(sess.CreatedAt) > 4*time.Hour,
		})
		addDirs = append(addDirs, sess.WorktreeDir)
	}

	// Build onboarding info.
	var onboardingInfo *agent.OnboardingInfo
	if params.Onboarding != nil {
		obState, _, obErr := params.Onboarding.Initialize(ctx)
		if obErr != nil {
			slog.Error("failed to initialize onboarding", "user", params.UserID, "err", obErr)
		}
		if obState != nil && obState.Phase != onboarding.PhaseComplete {
			var missing []string
			for _, f := range onboarding.AllInfoFields {
				if !obState.InfoGathered[f] {
					missing = append(missing, f)
				}
			}
			nextArea := onboarding.NextArea(obState.TipsShown)
			var nextAreaID string
			if nextArea != nil {
				nextAreaID = nextArea.ID
			}
			remaining := onboarding.UnshownAreas(obState.TipsShown)
			var remainingAreas []agent.OnboardingFeatureArea
			for _, area := range remaining {
				remainingAreas = append(remainingAreas, agent.OnboardingFeatureArea{
					ID:          area.ID,
					Name:        area.Name,
					Description: area.Description,
				})
			}
			onboardingInfo = &agent.OnboardingInfo{
				Phase:          string(obState.Phase),
				InfoGathered:   obState.InfoGathered,
				InfoMissing:    missing,
				TipsShown:      len(obState.TipsShown),
				TipsTotal:      len(onboarding.FeatureAreas),
				NextTip:        nextAreaID,
				RemainingAreas: remainingAreas,
			}
		}
	}

	systemPrompt := agent.BuildSystemPrompt(chInfos, devSessionInfos, params.BasePrompt, onboardingInfo)

	return IterationPromptResult{
		SystemPrompt: systemPrompt,
		AddDirs:      addDirs,
	}
}
