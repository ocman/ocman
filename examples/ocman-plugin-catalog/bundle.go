package main

import "embed"

// ui/dist holds the Vite build (make install-plugin PLUGIN=catalog runs it).
//
//go:embed all:ui/dist
var dist embed.FS

// fallback.js keeps a checkout without a UI build compilable and testable.
//
//go:embed fallback.js
var fallback []byte

func indexBundle() []byte {
	if data, err := dist.ReadFile("ui/dist/index.js"); err == nil && len(data) > 0 {
		return data
	}
	return fallback
}
