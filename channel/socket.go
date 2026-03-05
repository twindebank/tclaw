package channel

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
)

const SocketPath = "/tmp/tclaw.sock"

// SocketServer listens on a unix socket. Each connection is one turn:
// the client sends a message, we process it, write the response, close.
// Connections are paired with their messages so responses go to the
// right client even when later messages queue behind an active turn.
type SocketServer struct {
	path string

	mu      sync.Mutex
	conn    net.Conn   // connection for the current turn's response
	pending []net.Conn // queued connections waiting for their turn
}

func NewSocketServer(path string) *SocketServer {
	return &SocketServer{path: path}
}

func (s *SocketServer) Messages(ctx context.Context) <-chan string {
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

			// Read the full message (client closes write side when done).
			data, err := io.ReadAll(conn)
			slog.Debug("read complete", "bytes", len(data), "err", err)
			if err != nil || len(data) == 0 {
				if err != nil {
					slog.Warn("failed to read from connection", "err", err)
				}
				if err := conn.Close(); err != nil {
					slog.Warn("failed to close connection", "err", err)
				}
				continue
			}

			slog.Info("message received", "text", string(data))

			// Queue the connection so it's paired with its message.
			// Done will promote the next pending connection when a
			// turn finishes.
			s.mu.Lock()
			s.pending = append(s.pending, conn)
			s.mu.Unlock()

			select {
			case out <- string(data):
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

func (s *SocketServer) Send(_ context.Context, text string) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		return nil
	}
	_, err := io.WriteString(conn, text)
	return err
}

// Done closes the current turn's connection and promotes the next
// pending connection so Send writes to the right client.
func (s *SocketServer) Done(_ context.Context) error {
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
