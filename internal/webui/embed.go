// Package webui embeds the built Svelte SPA so the server ships as a single
// binary. The frontend build (SvelteKit adapter-static output) is copied into
// the dist/ directory before `go build`; in dev the directory is empty and the
// SPA is served by Vite instead.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS returns the embedded SPA build rooted at its top level, or nil if no build
// has been embedded (dev mode). adapter-static SPA mode emits 200.html as the
// fallback shell (there is no prerendered index.html), so that is the sentinel.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "200.html"); err != nil {
		return nil
	}
	return sub
}
