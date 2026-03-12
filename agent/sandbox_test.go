package agent

import (
	"context"
	"os/exec"
	"slices"
	"testing"
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

	// The wrapped command should be bwrap.
	if wrapped.Path != wrapped.Args[0] {
		// exec.CommandContext sets Path from the first arg
	}
	if wrapped.Args[0] != "bwrap" {
		t.Fatalf("expected bwrap command, got %q", wrapped.Args[0])
	}

	args := wrapped.Args

	// Should contain read-only binds for system paths.
	for _, p := range paths.ReadOnly {
		if !slices.Contains(args, p) {
			t.Errorf("expected read-only path %q in bwrap args", p)
		}
	}

	// Should contain read-write binds for user paths.
	// Format: --bind /path /path (source then dest, both the same).
	for _, p := range paths.ReadWrite {
		found := false
		for i, arg := range args {
			if arg == "--bind" && i+2 < len(args) && args[i+1] == p && args[i+2] == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected --bind %s %s in bwrap args", p, p)
		}
	}

	// Should contain --chdir for the working directory.
	chIdx := slices.Index(args, "--chdir")
	if chIdx < 0 {
		t.Fatal("expected --chdir in bwrap args")
	}
	if args[chIdx+1] != "/data/tclaw/theo/memory" {
		t.Errorf("expected --chdir /data/tclaw/theo/memory, got %q", args[chIdx+1])
	}

	// Should contain --share-net (MCP needs localhost).
	if !slices.Contains(args, "--share-net") {
		t.Error("expected --share-net in bwrap args")
	}

	// Should contain --die-with-parent.
	if !slices.Contains(args, "--die-with-parent") {
		t.Error("expected --die-with-parent in bwrap args")
	}

	// Should end with the original command after --.
	sepIdx := slices.Index(args, "--")
	if sepIdx < 0 {
		t.Fatal("expected -- separator in bwrap args")
	}
	trailing := args[sepIdx+1:]
	expected := []string{"claude", "--print", "--output-format", "stream-json", "-p", "hello"}
	if !slices.Equal(trailing, expected) {
		t.Errorf("expected original command after --, got %v", trailing)
	}

	// Should inherit the original env.
	if !slices.Equal(wrapped.Env, original.Env) {
		t.Errorf("expected inherited env, got %v", wrapped.Env)
	}

	// Wrapped cmd should not have Dir set (bwrap handles it via --chdir).
	if wrapped.Dir != "" {
		t.Errorf("expected empty Dir on wrapped cmd, got %q", wrapped.Dir)
	}
}

func TestSandboxEnabled_MatchesPlatform(t *testing.T) {
	// This test just documents the behavior — sandboxEnabled() returns
	// true only on Linux. On macOS (where tests typically run), it's false.
	enabled := sandboxEnabled()
	t.Logf("sandboxEnabled() = %v (GOOS=%s)", enabled, "runtime")

	// We can't change GOOS at runtime, so just verify it doesn't panic
	// and returns a bool.
	_ = enabled
}
