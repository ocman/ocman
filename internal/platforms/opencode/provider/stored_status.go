package opencode

import "github.com/ocman/ocman/internal/db"

// SettleStoredSessionStatus reuses lifecycle fencing without re-reading native
// history or discovering ports. The event already identifies its source port.
func (a *Adapter) SettleStoredSessionStatus(id, port string, inferred db.SessionStatus) db.SessionStatus {
	return a.settleStatusOnPort(id, port, inferred)
}
