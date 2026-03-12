package cli

import (
	"fmt"
	"os"
)

const dockerUsage = `Usage: tclaw docker [command]

Commands:
  build              Build the Docker image
  up                 Start the container (docker compose up)
  down               Stop the container (docker compose down)
  chat               Connect a chat session to the running container
`

func runDocker() {
	subcommand := ""
	if len(os.Args) >= 3 {
		subcommand = os.Args[2]
	}

	switch subcommand {
	case "build":
		fmt.Println("→ building docker image...")
		run("docker", "build", "-t", "tclaw", ".")
	case "up":
		fmt.Println("→ starting container...")
		run("docker", "compose", "up", "--build", "-d")
	case "down":
		fmt.Println("→ stopping container...")
		run("docker", "compose", "down")
	case "chat":
		run("docker", "compose", "exec", "agent", "tclaw-chat")
	case "--help", "-h", "help", "":
		fmt.Print(dockerUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown docker command: %q\n\n", subcommand)
		fmt.Fprint(os.Stderr, dockerUsage)
		os.Exit(1)
	}
}
