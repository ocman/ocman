package server

import (
	"runtime/debug"

	log "github.com/sirupsen/logrus"
)

// runWithRecover runs body while protecting the caller from a panic.
// On a panic, it logs at ERROR with the loop name, the panic value,
// and a stack trace, then returns. The caller is expected to be a
// loop that will run body again on its next tick — so a single
// panicking iteration cannot silently disable a feature for the rest
// of the process lifetime (FR-11).
//
// The name argument shows up in the structured log as `loop=...`,
// making it easy to grep for which background goroutine misbehaved.
func runWithRecover(name string, body func()) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{
				"loop":  name,
				"panic": r,
				"stack": string(debug.Stack()),
			}).Error("background loop panicked, continuing on next tick")
		}
	}()
	body()
}
