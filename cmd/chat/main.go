package main

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const socketPath = "/tmp/tclaw.sock"

// Styles
var (
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dividerChar = "─"
)

// Messages sent to the bubbletea event loop.
type chunkMsg string

func waitForChunk(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return nil
		}
		return chunkMsg(text)
	}
}

type model struct {
	input    textinput.Model
	viewport viewport.Model
	content  strings.Builder
	ready    bool
	width    int

	// incoming receives response chunks from background socket readers.
	incoming chan string
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (stop to cancel)"
	ti.Focus()

	return model{
		input:    ti,
		incoming: make(chan string, 256),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		waitForChunk(m.incoming),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			m.input.Reset()

			m.content.WriteString(userStyle.Render("> "+text) + "\n")
			m.viewport.SetContent(m.content.String())
			m.viewport.GotoBottom()

			go sendMessage(m.incoming, text)
		}

	case chunkMsg:
		m.content.WriteString(string(msg))
		m.viewport.SetContent(m.content.String())
		m.viewport.GotoBottom()
		cmds = append(cmds, waitForChunk(m.incoming))

	case tea.WindowSizeMsg:
		m.width = msg.Width
		inputHeight := 3 // input + divider + padding
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-inputHeight)
			m.viewport.SetContent(m.content.String())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - inputHeight
		}
		m.input.Width = msg.Width - 4
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.ready {
		return "connecting..."
	}

	divider := dimStyle.Render(strings.Repeat(dividerChar, m.width))

	return fmt.Sprintf("%s\n%s\n%s",
		m.viewport.View(),
		divider,
		m.input.View(),
	)
}

// sendMessage dials the agent socket, sends the message, and streams
// response chunks back into the incoming channel.
func sendMessage(incoming chan<- string, text string) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		incoming <- fmt.Sprintf("[error: %v]\n", err)
		return
	}
	defer conn.Close()

	fmt.Fprint(conn, text)
	conn.(*net.UnixConn).CloseWrite()

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			incoming <- string(buf[:n])
		}
		if err != nil {
			break
		}
	}
	// Visual separator after response ends.
	incoming <- "\n"
}

func main() {
	p := tea.NewProgram(
		initialModel(),
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
