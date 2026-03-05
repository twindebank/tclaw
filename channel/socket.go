package channel

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
)

const SocketPath = "/tmp/personal-agent.sock"

// SocketServer listens on a unix socket. Each connection is one turn:
// the client sends a message, we process it, write the response, close.
// Because the agent handles one message at a time, we track the active
// connection so Send knows where to write.
type SocketServer struct {
	path string
	mu   sync.Mutex
	conn net.Conn // active connection for the current turn
}

func NewSocketServer(path string) *SocketServer {
	return &SocketServer{path: path}
}

func (s *SocketServer) Messages(ctx context.Context) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)

		os.Remove(s.path)
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
			l.Close()
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

			// Read the full message (client closes write side when done).
			data, err := io.ReadAll(conn)
			if err != nil || len(data) == 0 {
				conn.Close()
				continue
			}

			slog.Info("message received", "text", string(data))

			// Store the connection so Send can write back to it.
			s.mu.Lock()
			s.conn = conn
			s.mu.Unlock()

			select {
			case out <- string(data):
			case <-ctx.Done():
				conn.Close()
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
	conn.Close()

	s.mu.Lock()
	s.conn = nil
	s.mu.Unlock()

	return err
}
