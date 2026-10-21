package conversationingest

import "time"

// AnalyticsInstances includes initialized local sources even while a scan is
// incomplete. Only committed projections are read; completeness is diagnostic,
// not permission to discard unrelated metrics. ReadyInstances remains strict.
func (m *Module) AnalyticsInstances() ([]string, bool) {
	_, complete := m.ReadyInstances()
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	ids := make([]string, 0, len(m.workers))
	for _, w := range m.workers {
		if id := m.instances[w.source.AdapterID]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids, complete
}

type readySource struct {
	instance string
	at       time.Time
}

// ReadyInstances returns a complete, recently scanned set of sources. It is
// deliberately empty after restart until every source completes a clean pass.
// A failed or deferred read disqualifies the entire set, not just that source.
func (m *Module) ReadyInstances() ([]string, bool) {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	if len(m.workers) == 0 || len(m.ready) != len(m.workers) {
		return nil, false
	}
	ids := make([]string, 0, len(m.workers))
	for _, w := range m.workers {
		r, ok := m.ready[w.source.AdapterID]
		if !ok || time.Since(r.at) > 2*m.deps.PollInterval {
			return nil, false
		}
		ids = append(ids, r.instance)
	}
	return ids, true
}

func (w *sourceWorker) readiness(ok bool) {
	m := w.module
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	if m.ready == nil {
		m.ready = map[string]readySource{}
	}
	delete(m.ready, w.source.AdapterID)
	if ok && w.instance.ID != "" {
		m.ready[w.source.AdapterID] = readySource{instance: w.instance.ID, at: time.Now()}
	}
}
