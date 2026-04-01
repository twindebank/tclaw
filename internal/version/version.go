package version

// Commit is the git commit hash embedded at build time via ldflags:
//
//	go build -ldflags "-X tclaw/version.Commit=$(git rev-parse --short HEAD)"
//
// Set to "dev" when building without ldflags (e.g. local go run).
var Commit = "dev"
