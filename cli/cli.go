package cli

import (
	"fmt"
	"os"
)

const usage = `tclaw — multi-user Claude Code host

Usage: tclaw <command> [args]

Commands:
  serve              Start the agent server
  serve --dev        Start with hot reload (requires air)
  chat               Connect a TUI chat session to the running server
  secret             Manage secrets in the OS keychain
  logs               Show recent Fly.io logs (snapshot, most recent last)
  logs -f            Follow (stream) Fly.io logs
  build              Build all binaries into bin/
  install            Install tclaw and tclaw-chat to $GOPATH/bin
  tidy               Run go mod tidy across all modules
  deploy             Build and deploy to Fly.io
  deploy secrets     Push keychain secrets to Fly.io
  deploy suspend     Spin down the Fly.io deployment
  deploy resume      Spin up the Fly.io deployment
  docker             Build, start, stop Docker containers

Run "tclaw <command> --help" for more information on a command.
`

// Run dispatches to the appropriate subcommand based on os.Args.
func Run() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "serve":
		runServe()
	case "chat":
		runChat()
	case "secret":
		runSecret()
	case "build":
		runBuild()
	case "install":
		runInstall()
	case "tidy":
		runTidy()
	case "logs":
		runLogs()
	case "deploy":
		runDeploy()
	case "docker":
		runDocker()
	case "--help", "-h", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", command)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
