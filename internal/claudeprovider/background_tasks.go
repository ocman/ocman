package claudeprovider

import (
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func backgroundCountEvent(id string, count int) providerprotocol.Event {
	props, _ := json.Marshal(map[string]any{"sessionID": id, "count": count})
	return providerprotocol.Event{Type: "ocman.session.background", Properties: props}
}

func (l *live) publishBackgroundCount(p *proc, count int) {
	// Stop or a replacement child may have overtaken the task update.
	_, err := l.sup.PublishIf(func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.procs[p.id] == p && p.busy && len(p.tasks) == count
	}, func(providerprotocol.Event) {}, backgroundCountEvent(p.id, count))
	if err != nil {
		l.publishFailed(p.id, err)
	}
}
