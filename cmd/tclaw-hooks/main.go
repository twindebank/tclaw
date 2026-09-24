// Command tclaw-hooks runs the Claude Code hooks tclaw registers on the agent's
// subprocess. It is a separate binary because a hook runs on every tool call:
// starting the whole tclaw server to answer one would be paid for on each one.
package main

import (
	"os"

	"tclaw/internal/hooks"
)

func main() {
	hooks.Run(os.Args)
}
