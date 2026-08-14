package gitproxy

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// commandSectionLimit caps how much of a push body is buffered while reading the
// ref commands. The commands are a handful of short lines at the head of the
// request; the packfile that follows is streamed untouched. A body whose command
// section exceeds this is refused rather than buffered — a push that needs more
// than this is not a shape we understand, and guessing would mean guessing about
// what is being written.
const commandSectionLimit = 64 * 1024

// zeroOID is the all-zeros object ID git uses for "no such object": as the old
// ID it means a ref is being created, as the new ID it means one is deleted.
const zeroOID = "0000000000000000000000000000000000000000"

// refCommand is one ref update from a git-receive-pack request.
type refCommand struct {
	OldOID string
	NewOID string
	Ref    string
}

// IsDelete reports whether the command removes the ref.
func (c refCommand) IsDelete() bool {
	return c.NewOID == zeroOID
}

// parseRefCommands reads the ref update commands from the head of a
// git-receive-pack body, returning them along with the bytes consumed so the
// caller can replay them upstream.
//
// The body is a series of pkt-lines — four hex length digits covering the whole
// line, then the payload — terminated by a flush packet ("0000"), after which
// the packfile follows. Each command reads:
//
//	<old-oid> SP <new-oid> SP <ref-name> [NUL capabilities]
//
// Any deviation is an error rather than a best guess: the caller refuses the
// push, since a body we cannot read is a body whose effects we cannot bound.
func parseRefCommands(body io.Reader) (commands []refCommand, consumed []byte, err error) {
	// Deliberately unbuffered: a bufio.Reader here would read ahead into the
	// packfile that follows the commands, and those read-ahead bytes would be
	// lost once this function returns and the buffer goes out of scope,
	// corrupting the push forwarded upstream. io.ReadFull already loops over
	// partial reads on a plain reader, so buffering buys nothing.
	reader := io.LimitReader(body, commandSectionLimit)
	var seen []byte

	for {
		lengthHex := make([]byte, 4)
		n, readErr := io.ReadFull(reader, lengthHex)
		seen = append(seen, lengthHex[:n]...)
		if readErr != nil {
			return nil, nil, fmt.Errorf("read pkt-line length: %w", readErr)
		}

		length, decodeErr := parsePktLength(lengthHex)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}

		// Flush packet — the command section ends and the packfile begins.
		if length == 0 {
			return commands, seen, nil
		}
		if length < 4 {
			return nil, nil, fmt.Errorf("pkt-line length %d is below the 4-byte header", length)
		}

		payload := make([]byte, length-4)
		n, readErr = io.ReadFull(reader, payload)
		seen = append(seen, payload[:n]...)
		if readErr != nil {
			return nil, nil, fmt.Errorf("read pkt-line payload: %w", readErr)
		}

		command, parseErr := parseRefCommand(string(payload))
		if parseErr != nil {
			return nil, nil, parseErr
		}
		commands = append(commands, command)
	}
}

// parsePktLength decodes the four-digit hex length prefix of a pkt-line.
func parsePktLength(raw []byte) (int, error) {
	decoded, err := hex.DecodeString(string(raw))
	if err != nil || len(decoded) != 2 {
		return 0, fmt.Errorf("malformed pkt-line length %q", string(raw))
	}
	return int(decoded[0])<<8 | int(decoded[1]), nil
}

// parseRefCommand parses one "<old> <new> <ref>" command payload. Capabilities
// after a NUL byte on the first command are ignored.
func parseRefCommand(payload string) (refCommand, error) {
	if idx := strings.IndexByte(payload, 0); idx >= 0 {
		payload = payload[:idx]
	}
	payload = strings.TrimRight(payload, "\n")

	fields := strings.Fields(payload)
	if len(fields) != 3 {
		return refCommand{}, fmt.Errorf("malformed ref command %q", payload)
	}
	return refCommand{OldOID: fields[0], NewOID: fields[1], Ref: fields[2]}, nil
}

// checkPullRequestsOnly reports why a push is not allowed under the
// pull_requests_only tier, or nil when every command is acceptable.
//
// The rule is that the default branch must only change through a reviewed pull
// request, so any write to it — create, update, or delete — is refused. Feature
// branches are the agent's to push, rewrite and delete. Tags and other refs are
// refused too: they are not needed to open a PR, and moving a release tag is as
// consequential as writing the branch itself.
func checkPullRequestsOnly(commands []refCommand, defaultBranch string) error {
	if len(commands) == 0 {
		return fmt.Errorf("push contained no ref updates")
	}

	defaultRef := "refs/heads/" + defaultBranch
	for _, c := range commands {
		switch {
		case c.Ref == defaultRef:
			return fmt.Errorf("pushing to the default branch %q is not allowed at this access level — "+
				"push a branch and open a pull request instead", defaultBranch)
		case !strings.HasPrefix(c.Ref, "refs/heads/"):
			return fmt.Errorf("pushing %q is not allowed at this access level — only branches may be pushed", c.Ref)
		}
	}
	return nil
}
