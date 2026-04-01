package agent

import (
	"context"
	"os/exec"
	"runtime"
)

// sandboxEnabled returns true when the subprocess should be sandboxed.
// Only enabled on Linux (deployed environments) — macOS (local dev) skips it.
func sandboxEnabled() bool {
	return runtime.GOOS == "linux"
}

// sandboxPaths defines the filesystem paths exposed inside the sandbox.
type sandboxPaths struct {
	// ReadWrite paths the subprocess can read and write (user dirs).
	ReadWrite []string
	// ReadOnly system paths the subprocess needs to function.
	ReadOnly []string

	// ReadOnlyOverlay paths are bound read-only AFTER read-write paths,
	// allowing specific files within read-write directories to be protected.
	// Use this to lock down individual files (e.g. settings.json) inside
	// an otherwise writable directory.
	ReadOnlyOverlay []string
}

// systemReadOnlyPaths are the minimal system paths needed for the claude CLI
// (a Node.js binary) to function: shared libraries, TLS certs, DNS, timezone,
// locale, and the node/claude binaries themselves.
var systemReadOnlyPaths = []string{
	"/usr",
	"/bin",
	"/lib",
	"/lib64",
	"/etc/ssl",
	"/etc/resolv.conf",
	"/etc/hosts",
	"/etc/nsswitch.conf",
	"/etc/localtime",
	"/etc/passwd", // getpwnam for USER lookups
	"/etc/group",  // getgrnam
	"/usr/share/zoneinfo",
	"/usr/local/bin", // claude, gws binaries
	"/usr/local/lib", // node modules (npm -g)
	"/usr/local/go",  // Go toolchain for dev sessions (go test, go build)
}

// wrapWithSandbox prepends bubblewrap arguments to run the command inside a
// restricted mount namespace. Only the explicitly listed paths are visible.
// Returns a new *exec.Cmd — the original is not modified.
//
// The sandbox:
//   - Creates new mount, PID, and UTS namespaces (NOT network — MCP needs localhost)
//   - Binds user dirs read-write, system dirs read-only
//   - Provides /proc, /dev, and a private /tmp
//   - Dies with the parent process
func wrapWithSandbox(ctx context.Context, original *exec.Cmd, paths sandboxPaths) *exec.Cmd {
	var bwrapArgs []string

	// Read-only system paths. Use --ro-bind-try so missing paths
	// (e.g. /lib64 on some distros) don't cause a hard failure.
	for _, p := range paths.ReadOnly {
		bwrapArgs = append(bwrapArgs, "--ro-bind-try", p, p)
	}

	// Read-write user paths.
	for _, p := range paths.ReadWrite {
		bwrapArgs = append(bwrapArgs, "--bind", p, p)
	}

	// Read-only overlays — protect specific files within read-write dirs.
	// These are bound AFTER read-write paths so bwrap overlays them,
	// making the files immutable even though their parent dir is writable.
	for _, p := range paths.ReadOnlyOverlay {
		bwrapArgs = append(bwrapArgs, "--ro-bind-try", p, p)
	}

	// Kernel filesystems and private /tmp.
	bwrapArgs = append(bwrapArgs,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	)

	// Namespace isolation.
	bwrapArgs = append(bwrapArgs,
		"--unshare-pid", // own PID namespace
		"--unshare-uts", // own hostname
		"--share-net",   // keep network (MCP server on localhost)
		"--die-with-parent",
	)

	// Set CWD inside the sandbox.
	if original.Dir != "" {
		bwrapArgs = append(bwrapArgs, "--chdir", original.Dir)
	}

	// Separator and the actual command.
	bwrapArgs = append(bwrapArgs, "--")
	bwrapArgs = append(bwrapArgs, original.Args...)

	cmd := exec.CommandContext(ctx, "bwrap", bwrapArgs...)
	cmd.Env = original.Env
	cmd.Dir = "" // bwrap handles CWD via --chdir
	cmd.Stdin = original.Stdin
	cmd.Stdout = original.Stdout
	cmd.Stderr = original.Stderr
	return cmd
}
