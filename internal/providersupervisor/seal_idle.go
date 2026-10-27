package providersupervisor

import (
	"encoding/json"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// sealIdle commits an idle cleanup pause to replacement. Once sealed, a
// concurrent reconnect cannot undo the drain through /resume before Stop.
func (s *Supervisor) sealIdle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := r.URL.Query().Get("token")
	idle := token != "" && s.draining && s.idleDrainToken == token && s.admitted == 0
	for _, p := range s.sessions {
		if p.active || p.limited || len(p.pending) != 0 {
			idle = false
		}
	}
	if idle {
		s.idleDrainToken = ""
	}
	_ = json.NewEncoder(w).Encode(providerprotocol.DrainState{Idle: idle})
}
