package onboarding

// FeatureArea is a topic the agent can teach during onboarding.
// The agent generates the actual tip content on the fly —
// these just define what areas exist and haven't been covered yet.
type FeatureArea struct {
	ID          string
	Name        string
	Description string // brief hint for the agent about what to cover
}

// FeatureAreas is the set of features the agent can introduce during
// the tips phase. Order is a suggestion, not a requirement.
var FeatureAreas = []FeatureArea{
	// Universally useful — shown first.
	{ID: "memory", Name: "Memory system", Description: "CLAUDE.md, topic files, @references, what gets loaded automatically"},
	{ID: "scheduling", Name: "Scheduled prompts", Description: "cron schedules, recurring tasks, daily briefings"},
	{ID: "web_search", Name: "Web access", Description: "search, fetch pages, weather, news, prices, current events"},
	{ID: "connections", Name: "Service connections", Description: "Google Workspace, Monzo, built-in providers vs remote MCPs"},
	{ID: "channels", Name: "Multiple channels", Description: "separate Telegram bots, roles, per-channel tool permissions"},
	{ID: "channel_management", Name: "Channel management", Description: "creating channels at runtime, tool groups, cross-channel messaging"},
	{ID: "remote_mcps", Name: "MCP ecosystem", Description: "remote MCP servers, directory of available services"},
	{ID: "compact_reset", Name: "Context management", Description: "compact command, reset options, session management"},
	{ID: "dev_workflow", Name: "Dev workflow", Description: "self-modification, git worktrees, PRs, deployment"},

	// Optional / regional — shown later.
	{ID: "tfl", Name: "Transport for London", Description: "line status, journey planning, arrivals, commute checks"},
	{ID: "restaurant", Name: "Restaurant reservations", Description: "search, availability, booking via Resy"},
	{ID: "banking", Name: "Open Banking", Description: "connect UK bank accounts, view balances and transactions via Enable Banking"},
	{ID: "telegram_client", Name: "Telegram management", Description: "create bots, manage chats, search messages via Telegram Client API"},
}

// UnshownAreas returns feature areas that haven't been shown yet.
func UnshownAreas(shown []string) []FeatureArea {
	shownSet := make(map[string]bool, len(shown))
	for _, id := range shown {
		shownSet[id] = true
	}
	var remaining []FeatureArea
	for _, area := range FeatureAreas {
		if !shownSet[area.ID] {
			remaining = append(remaining, area)
		}
	}
	return remaining
}

// NextArea returns the next feature area to cover, or nil if all done.
func NextArea(shown []string) *FeatureArea {
	remaining := UnshownAreas(shown)
	if len(remaining) == 0 {
		return nil
	}
	return &remaining[0]
}
