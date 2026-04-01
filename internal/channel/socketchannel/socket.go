package socketchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/id"
)

// maxMessageSize is the maximum bytes we'll read from a single socket
// connection. Anything larger is truncated to prevent memory exhaustion.
const maxMessageSize = 64 * 1024 // 64 KiB

// Server listens on a unix socket. Each connection is one turn:
// the client sends a message, we process it, write the response, close.
// Connections are paired with their messages so responses go to the
// right client even when later messages queue behind an active turn.
type Server struct {
	path        string
	name        string
	description string
	purpose     string

	mu      sync.Mutex
	conn    net.Conn   // connection for the current turn's response
	pending []net.Conn // queued connections waiting for their turn
}

func NewServer(path, name, description, purpose string) *Server {
	return &Server{path: path, name: name, description: description, purpose: purpose}
}

func (s *Server) Info() channel.Info {
	return channel.Info{
		ID:          channel.ChannelID(s.path),
		Type:        channel.TypeSocket,
		Name:        s.name,
		Description: s.description,
		Purpose:     s.purpose,
	}
}

func (s *Server) Messages(ctx context.Context) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)

		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove old socket", "err", err)
		}
		l, err := net.Listen("unix", s.path)
		if err != nil {
			slog.Error("socket listen failed", "err", err)
			return
		}
		defer l.Close()
		slog.Info("listening", "socket", s.path)

		// Close listener when ctx is cancelled so Accept unblocks.
		go func() {
			<-ctx.Done()
			if err := l.Close(); err != nil {
				slog.Warn("failed to close listener on shutdown", "err", err)
			}
		}()

		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					slog.Warn("accept error", "err", err)
					continue
				}
			}
			slog.Debug("connection accepted")

			// Read the message (client closes write side when done).
			// LimitReader caps at maxMessageSize to prevent memory exhaustion.
			data, err := io.ReadAll(io.LimitReader(conn, maxMessageSize))
			if err != nil || len(data) == 0 {
				if err != nil {
					slog.Warn("failed to read from connection", "err", err)
				}
				if err := conn.Close(); err != nil {
					slog.Warn("failed to close connection", "err", err)
				}
				continue
			}

			text := strings.TrimSpace(string(data))
			if text == "" {
				slog.Debug("ignoring empty message")
				if err := conn.Close(); err != nil {
					slog.Warn("failed to close connection", "err", err)
				}
				continue
			}

			slog.Info("message received", "length", len(text))

			// Pair the connection with its message. If no turn is
			// active, promote it directly so Send writes to it.
			// Otherwise queue it for when the current turn finishes.
			s.mu.Lock()
			if s.conn == nil {
				s.conn = conn
			} else {
				s.pending = append(s.pending, conn)
			}
			s.mu.Unlock()

			select {
			case out <- text:
			case <-ctx.Done():
				if err := conn.Close(); err != nil {
					slog.Warn("failed to close connection on shutdown", "err", err)
				}
				return
			}
		}
	}()
	return out
}

func (s *Server) Send(_ context.Context, text string) (channel.MessageID, error) {
	s.mu.Lock()
	conn := s.conn
	msgID := channel.MessageID(id.Generate("message"))
	s.mu.Unlock()

	if conn == nil {
		return msgID, nil
	}
	return msgID, s.writeWireMsg(conn, wireMsg{Op: "send", ID: string(msgID), Text: text})
}

func (s *Server) Edit(_ context.Context, msgID channel.MessageID, text string) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		return nil
	}
	return s.writeWireMsg(conn, wireMsg{Op: "edit", ID: string(msgID), Text: text})
}

// wireMsg is the JSON-line protocol between socket server and client.
type wireMsg struct {
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
	Text string `json:"text,omitempty"`
}

func (s *Server) writeWireMsg(conn net.Conn, msg wireMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal wire msg: %w", err)
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// Done closes the current turn's connection and promotes the next
// pending connection so Send writes to the right client.
func (s *Server) Done(_ context.Context) error {
	s.mu.Lock()
	old := s.conn
	if len(s.pending) > 0 {
		s.conn = s.pending[0]
		s.pending = s.pending[1:]
	} else {
		s.conn = nil
	}
	s.mu.Unlock()

	if old == nil {
		return nil
	}
	return old.Close()
}

func (s *Server) SplitStatusMessages() bool {
	return false
}

func (s *Server) Markup() channel.Markup {
	return channel.MarkupMarkdown
}

func (s *Server) StatusWrap() channel.StatusWrap {
	return channel.StatusWrap{}
}
