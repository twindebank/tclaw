package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"

	"personal-agent/channel"
)

// chat is a thin client that connects to the agent's unix socket.
// Type a message, get a response. All agent logs stay in the agent terminal.
func main() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		msg := scanner.Text()
		if msg == "" {
			fmt.Print("> ")
			continue
		}

		conn, err := net.Dial("unix", channel.SocketPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: could not connect to agent (%v)\nIs it running? Try: make agent\n", err)
			os.Exit(1)
		}

		// Send message then close write side so agent gets EOF.
		fmt.Fprint(conn, msg)
		conn.(*net.UnixConn).CloseWrite()

		// Read response until agent closes the connection.
		resp, err := io.ReadAll(conn)
		conn.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading response: %v\n", err)
		} else {
			fmt.Printf("\n%s\n", resp)
		}

		fmt.Print("> ")
	}
}
