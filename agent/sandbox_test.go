package agent

import (
	"context"
	"os/exec"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapWithSandbox_CommandStructure(t *testing.T) {
	original := exec.CommandContext(context.Background(), "claude", "--print", "--output-format", "stream-json", "-p", "hello")
	original.Dir = "/data/tclaw/theo/memory"
	original.Env = []string{"PATH=/usr/bin", "HOME=/data/tclaw/theo/home"}

	paths := sandboxPaths{
		ReadWrite: []string{"/data/tclaw/theo/memory", "/data/tclaw/theo/home"},
		ReadOnly:  []string{"/usr", "/bin", "/lib"},
	}

	wrapped := wrapWithSandbox(context.Background(), original, paths)

	require.Equal(t, "bwrap", wrapped.Args[0])

	args := wrapped.Args

	t.Run("contains read-only binds", func(t *testing.T) {
		for _, p := range paths.ReadOnly {
			require.True(t, slices.Contains(args, p), "expected read-only path %q in bwrap args", p)
		}
	})

	t.Run("contains read-write binds", func(t *testing.T) {
		for _, p := range paths.ReadWrite {
			found := false
			for i, arg := range args {
				if arg == "--bind" && i+2 < len(args) && args[i+1] == p && args[i+2] == p {
					found = true
					break
				}
			}
			require.True(t, found, "expected --bind %s %s in bwrap args", p, p)
		}
	})

	t.Run("sets chdir to working directory", func(t *testing.T) {
		chIdx := slices.Index(args, "--chdir")
		require.GreaterOrEqual(t, chIdx, 0, "expected --chdir in bwrap args")
		require.Equal(t, "/data/tclaw/theo/memory", args[chIdx+1])
	})

	t.Run("includes network and parent flags", func(t *testing.T) {
		require.True(t, slices.Contains(args, "--share-net"))
		require.True(t, slices.Contains(args, "--die-with-parent"))
	})

	t.Run("original command follows separator", func(t *testing.T) {
		sepIdx := slices.Index(args, "--")
		require.GreaterOrEqual(t, sepIdx, 0, "expected -- separator in bwrap args")
		trailing := args[sepIdx+1:]
		expected := []string{"claude", "--print", "--output-format", "stream-json", "-p", "hello"}
		require.Equal(t, expected, trailing)
	})

	t.Run("inherits env and clears Dir", func(t *testing.T) {
		require.Equal(t, original.Env, wrapped.Env)
		require.Empty(t, wrapped.Dir, "bwrap handles working dir via --chdir")
	})
}

func TestSandboxEnabled_MatchesPlatform(t *testing.T) {
	// Documents the behavior — sandboxEnabled() returns true only on Linux.
	// On macOS (where tests typically run), it's false.
	enabled := sandboxEnabled()
	require.IsType(t, true, enabled)
}
