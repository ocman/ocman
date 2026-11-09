// Package keybindings owns the hub's keyboard binding catalog.
package keybindings

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
)

// Defaults stay backend-owned. A future config-file loader can replace this
// source without changing action registration or dispatch in the browser.
//go:embed defaults.json
var defaults []byte

type Module struct{}

func (*Module) Name() string { return "keybindings" }

func (*Module) Routes(r module.Router) {
	r.Handle("GET /api/keybindings", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, json.RawMessage(defaults))
	})
}
