package scheduletools

import (
	"tclaw/mcp"
	"tclaw/schedule"
)

// Deps holds dependencies for schedule management tools.
type Deps struct {
	Store     *schedule.Store
	Scheduler *schedule.Scheduler
}

// RegisterTools adds schedule management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(scheduleCreateDef(), scheduleCreateHandler(deps))
	handler.Register(scheduleListDef(), scheduleListHandler(deps))
	handler.Register(scheduleEditDef(), scheduleEditHandler(deps))
	handler.Register(scheduleDeleteDef(), scheduleDeleteHandler(deps))
	handler.Register(schedulePauseDef(), schedulePauseHandler(deps))
	handler.Register(scheduleResumeDef(), scheduleResumeHandler(deps))
}
