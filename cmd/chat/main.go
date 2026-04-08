package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"
)

// socketPath is resolved at startup via the user picker.
var socketPath string

const defaultConfigPath = "tclaw.yaml"

// channelTypeSocket matches channel.TypeSocket without importing the full
// internal module — this binary deliberately avoids tclaw's dependency tree.
const channelTypeSocket = "socket"

// Styles
var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	timestampStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	accentStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dividerChar    = "─"
)

// wireMsg matches the JSON-line protocol from the socket server.
type wireMsg struct {
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
	Text string `json:"text,omitempty"`
}

// chatMsg is a single message in the conversation (user or agent).
type chatMsg struct {
	isUser    bool
	text      string
	timestamp string
}

// Messages sent to the bubbletea event loop.
type wireMsgEvent wireMsg
type connClosedEvent struct{}

func waitForWireMsg(ch <-chan wireMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return connClosedEvent{}
		}
		return wireMsgEvent(msg)
	}
}

// messages tracks ordered chat messages, keyed by ID for edits.
// Uses pointer to survive bubbletea's value-copy semantics.
type messages struct {
	order []string            // message IDs in display order
	byID  map[string]*chatMsg // content by ID
	seq   int                 // counter for user message IDs
}

func newMessages() *messages {
	return &messages{
		byID: make(map[string]*chatMsg),
	}
}

func (ms *messages) addUser(text string) {
	ms.seq++
	id := fmt.Sprintf("user-%d", ms.seq)
	msg := &chatMsg{isUser: true, text: text, timestamp: time.Now().Format("15:04:05")}
	ms.order = append(ms.order, id)
	ms.byID[id] = msg
}

func (ms *messages) send(id, text string) {
	msg := &chatMsg{text: text, timestamp: time.Now().Format("15:04:05")}
	ms.order = append(ms.order, id)
	ms.byID[id] = msg
}

func (ms *messages) edit(id, text string) {
	if existing, ok := ms.byID[id]; ok {
		existing.text = text
	}
}

func (ms *messages) render() string {
	var b strings.Builder
	for i, id := range ms.order {
		msg := ms.byID[id]
		if i > 0 {
			b.WriteString("\n")
		}
		ts := timestampStyle.Render(msg.timestamp)
		if msg.isUser {
			b.WriteString(ts + " " + userStyle.Render("> "+msg.text) + "\n")
		} else {
			// Extract bare URLs from markdown links so they're on their
			// own line — clickable and copyable in the terminal.
			text := flattenMarkdownLinks(msg.text)
			b.WriteString(ts + " " + text)
		}
	}
	return b.String()
}

type model struct {
	input    textinput.Model
	viewport viewport.Model
	msgs     *messages
	ready    bool
	width    int

	// incoming receives wire protocol messages from background socket readers.
	incoming chan wireMsg
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (stop to cancel)"
	ti.Focus()

	return model{
		input:    ti,
		msgs:     newMessages(),
		incoming: make(chan wireMsg, 256),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return textinput.Blink() },
		waitForWireMsg(m.incoming),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			m.input.Reset()
			m.msgs.addUser(text)
			m.viewport.SetContent(m.msgs.render())
			m.viewport.GotoBottom()

			go sendMessage(m.incoming, text)
		}

	case wireMsgEvent:
		switch msg.Op {
		case "send":
			m.msgs.send(msg.ID, msg.Text)
		case "edit":
			m.msgs.edit(msg.ID, msg.Text)
		}
		m.viewport.SetContent(m.msgs.render())
		m.viewport.GotoBottom()
		cmds = append(cmds, waitForWireMsg(m.incoming))

	case connClosedEvent:
		// Connection closed, listen for next message.
		cmds = append(cmds, waitForWireMsg(m.incoming))

	case tea.WindowSizeMsg:
		m.width = msg.Width
		inputHeight := 3 // input + divider + padding
		if !m.ready {
			m.viewport = viewport.New(
				viewport.WithWidth(msg.Width),
				viewport.WithHeight(msg.Height-inputHeight),
			)
			m.viewport.SoftWrap = true
			m.viewport.SetContent(m.msgs.render())
			m.ready = true
		} else {
			m.viewport.SetWidth(msg.Width)
			m.viewport.SetHeight(msg.Height - inputHeight)
		}
		m.input.SetWidth(msg.Width - 4)
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	var content string
	if !m.ready {
		content = "connecting..."
	} else {
		divider := dimStyle.Render(strings.Repeat(dividerChar, m.width))
		content = fmt.Sprintf("%s\n%s\n%s",
			m.viewport.View(),
			divider,
			m.input.View(),
		)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

var markdownLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// flattenMarkdownLinks replaces [text](url) with "text:\n  url\n"
// so URLs land on their own line — clickable and copyable in terminals.
func flattenMarkdownLinks(s string) string {
	return markdownLinkRe.ReplaceAllString(s, "$1:\n  $2\n")
}

// sendMessage dials the agent socket, sends the message, and streams
// JSON-line response messages back into the incoming channel.
func sendMessage(incoming chan<- wireMsg, text string) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		incoming <- wireMsg{Op: "send", ID: "err", Text: fmt.Sprintf("[error: %v]\n", err)}
		return
	}
	defer conn.Close()

	fmt.Fprint(conn, text)
	conn.(*net.UnixConn).CloseWrite()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg wireMsg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		incoming <- msg
	}
}

// minimalConfig is just enough to extract user/channel info and base_dir
// from the shared tclaw config file. Avoids importing the tclaw module.
type minimalConfig struct {
	BaseDir string       `yaml:"base_dir"`
	Users   []configUser `yaml:"users"`
}

type configUser struct {
	ID       string          `yaml:"id"`
	Channels []configChannel `yaml:"channels"`
}

type configChannel struct {
	Type string `yaml:"type"`
	Name string `yaml:"name"`
}

func loadMinimalConfig(path string) (*minimalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Config is keyed by environment (local, prod, ...).
	// Chat is only used locally, so always read the "local" section.
	var envMap map[string]minimalConfig
	if err := yaml.Unmarshal(data, &envMap); err != nil {
		return nil, err
	}

	cfg, ok := envMap["local"]
	if !ok {
		return nil, fmt.Errorf("no 'local' environment found in config")
	}

	if cfg.BaseDir == "" {
		cfg.BaseDir = "/tmp/tclaw"
	}
	return &cfg, nil
}

// socketEntry is a connectable socket channel from config.
type socketEntry struct {
	userID    string
	channel   string
	socket    string
	listening bool // socket file exists (channel is up, agent starts on first message)
}

func discoverSockets(cfg *minimalConfig) []socketEntry {
	var entries []socketEntry
	for _, u := range cfg.Users {
		for _, ch := range u.Channels {
			if ch.Type != channelTypeSocket {
				continue
			}
			// Matches router.BuildChannels path derivation.
			sock := filepath.Join(cfg.BaseDir, u.ID, ch.Name+".sock")
			entries = append(entries, socketEntry{
				userID:    u.ID,
				channel:   ch.Name,
				socket:    sock,
				listening: canDial(sock),
			})
		}
	}
	return entries
}

// canDial checks if a unix socket is actually accepting connections,
// not just whether the file exists on disk.
func canDial(path string) bool {
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (e socketEntry) label() string {
	return e.userID + "/" + e.channel
}

func renderChannelList(sockets []socketEntry) {
	fmt.Print("\033[H\033[2J") // clear screen
	fmt.Println("Channels:")
	fmt.Println()
	for i, s := range sockets {
		status := accentStyle.Render("listening")
		if !s.listening {
			status = warnStyle.Render("offline")
		}
		fmt.Printf("  [%d] %s  %s\n", i+1, s.label(), status)
	}
	fmt.Println()
	fmt.Printf("Select [1-%d] (polling every 2s): ", len(sockets))
}

// pickSocket reads the config file, lists socket channels with their status,
// and prompts the operator to select one. Polls every 2s to refresh status.
func pickSocket(configPath string) string {
	cfg, err := loadMinimalConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config %s: %v\n", configPath, err)
		os.Exit(1)
	}

	sockets := discoverSockets(cfg)

	if len(sockets) == 0 {
		fmt.Fprintln(os.Stderr, "no socket channels defined in config")
		os.Exit(1)
	}

	// Single socket: wait for it to come online.
	if len(sockets) == 1 {
		s := sockets[0]
		if !s.listening {
			fmt.Printf("Waiting for %s to come online...\n", s.label())
			for !s.listening {
				time.Sleep(2 * time.Second)
				s.listening = canDial(s.socket)
			}
		}
		fmt.Printf("Connecting to %s\n", s.label())
		return s.socket
	}

	// Multiple sockets: show picker with polling.
	// Read input in a goroutine so we can refresh the display.
	inputCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			inputCh <- strings.TrimSpace(scanner.Text())
		}
		close(inputCh)
	}()

	renderChannelList(sockets)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case input, ok := <-inputCh:
			if !ok {
				fmt.Fprintln(os.Stderr, "no selection")
				os.Exit(1)
			}

			n, err := strconv.Atoi(input)
			if err != nil || n < 1 || n > len(sockets) {
				fmt.Fprintf(os.Stderr, "invalid selection: %q\n", input)
				os.Exit(1)
			}

			selected := sockets[n-1]
			if !selected.listening {
				fmt.Fprintf(os.Stderr, "%s: channel offline\n", selected.label())
				os.Exit(1)
			}

			fmt.Printf("Connecting to %s\n", selected.label())
			return selected.socket

		case <-ticker.C:
			sockets = discoverSockets(cfg)
			renderChannelList(sockets)
		}
	}
}

func main() {
	// If -socket is passed explicitly, skip the picker.
	for _, arg := range os.Args[1:] {
		if v, ok := strings.CutPrefix(arg, "-socket="); ok {
			socketPath = v
			goto run
		}
	}

	{
		configPath := defaultConfigPath
		if v := os.Getenv("TCLAW_CONFIG"); v != "" {
			configPath = v
		}
		socketPath = pickSocket(configPath)
	}

run:
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
