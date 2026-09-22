// Package web embeds the built dashboard (web/dist) into vaultd.
//
// Build the UI with `make web` before `make build`. When dist contains only
// the .keep placeholder, vaultd serves a short "UI not built" page.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built UI and whether it contains an index.html.
func Dist() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return sub, false
	}
	return sub, true
}
