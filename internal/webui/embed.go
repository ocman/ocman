// Package webui holds the built single-page app, embedded into the
// binary at compile time.
//
// It exists as its own package because two binaries serve the same
// bundle: the ocman server and the share relay. Keeping the embed here
// means the relay does not have to import the full server package (and
// with it the databases, tmux, and platform adapters) just to reach the
// static assets, and there is still exactly one frontend build.
//
// The build output lands in this package's static/ directory; see the
// frontend's vite config.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed static/*
var embedded embed.FS

// FS returns the app bundle rooted at the bundle itself, so "index.html"
// is a top-level entry.
func FS() (fs.FS, error) {
	return fs.Sub(embedded, "static")
}

// MustFS is FS for callers that cannot handle an error. It panics only
// if the embedded tree is malformed, which is a build-time fault.
func MustFS() fs.FS {
	sub, err := FS()
	if err != nil {
		panic("webui: embedded assets are malformed: " + err.Error())
	}
	return sub
}
