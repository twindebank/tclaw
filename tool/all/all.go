// Package all provides the complete list of tool packages that use the
// toolpkg.Registry for registration. Each package owns its own setup logic
// via Register() and OnCredentialSetChange() — the router doesn't need to
// know about any specific package.
package all

import (
	"tclaw/tool/credentialtools"
	"tclaw/tool/google"
	"tclaw/tool/monzo"
	"tclaw/tool/toolpkg"
)

// NewRegistry returns a registry containing all tool packages that participate
// in the unified registration system. Add new packages here — the router
// imports this package and calls NewRegistry() without needing to know about
// individual tool packages.
func NewRegistry() *toolpkg.Registry {
	return toolpkg.NewRegistry(
		&credentialtools.Package{},
		&google.Package{},
		&monzo.Package{},
	)
}
