package agent

import _ "embed"

// KnowledgeSkillTemplate is the SKILL.md seeded into a user's
// home/.claude/skills/knowledge/ when a personal knowledge base is configured.
// The {{path}} placeholder is replaced with the absolute knowledge clone path
// before writing. The claude CLI auto-discovers skills from ~/.claude/skills/.
//
//go:embed knowledge_skill.md
var KnowledgeSkillTemplate string
