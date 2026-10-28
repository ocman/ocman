package conversationingest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/tasksvc"
)

type Source struct {
	AdapterID string // routing identity, separate from physical source/platform type
	Label     string
	Reader    platforms.HistoryReader
}

type Deps struct {
	Store        *statev2.DB
	Tasks        *tasksvc.Manager
	OwnerID      string
	Sources      []Source
	PollInterval time.Duration
	// VerifyInterval is how often every session is reread to measure drift
	// that version hints missed. It persists across restarts.
	VerifyInterval time.Duration
	Debounce       time.Duration
	ReadTimeout    time.Duration
	// BackfillDutyPercent bounds background work relative to rest time. Zero
	// defaults to 20; 100 disables pauses but still serializes backfill sources.
	BackfillDutyPercent int
	OnError             func(instanceID, sessionID string, err error)
}

// Module maintains the authoritative history store. It never broadcasts historical idle events or
// calls the platform's mutating/live methods. Each source has an independent,
// serialized worker and all workers are joined before Run returns.
type Module struct {
	deps     Deps
	workers  []*sourceWorker
	backfill *backfillLimiter
	readyMu  sync.Mutex
	ready    map[string]readySource
	// receiptMu guards the read-approval broadcast and adapter instances.
	receiptMu     sync.Mutex
	receiptCh     chan struct{}
	instances     map[string]string
	readRequests  map[[2]string]uint64
	readValidated map[[2]string]uint64
}

func NewModule(deps Deps) *Module {
	if deps.PollInterval <= 0 {
		deps.PollInterval = 30 * time.Second
	}
	if deps.VerifyInterval <= 0 {
		deps.VerifyInterval = 6 * time.Hour
	}
	if deps.Debounce <= 0 {
		deps.Debounce = time.Second
	}
	if deps.ReadTimeout <= 0 {
		deps.ReadTimeout = 30 * time.Second
	}
	if deps.BackfillDutyPercent <= 0 || deps.BackfillDutyPercent > 100 {
		deps.BackfillDutyPercent = 20
	}
	m := &Module{deps: deps, backfill: newBackfillLimiter(deps.BackfillDutyPercent)}
	for _, source := range deps.Sources {
		if source.Reader != nil {
			m.workers = append(m.workers, &sourceWorker{module: m, source: source, wake: make(chan struct{}, 1),
				dirty: map[string]bool{}, versions: map[string]string{}, failing: map[string]string{}})
		}
	}
	return m
}

func (*Module) Name() string         { return "conversation-ingestion" }
func (*Module) Routes(module.Router) {}

func (m *Module) Start(context.Context) error {
	if m.deps.Store == nil || m.deps.OwnerID == "" {
		return fmt.Errorf("conversation ingestion requires a store and stable owner identity")
	}
	return nil
}

func (m *Module) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, w := range m.workers {
		workers.Go(func() { w.run(ctx) })
	}
	workers.Wait()
}

// Notify coalesces invalidations without reading platforms or blocking event
// producers on database I/O. Missing legacy routing identities wake all local
// sources; each still reads and writes only its own physical source identity.
func (m *Module) Notify(adapterID, sessionID string) {
	m.notify(adapterID, sessionID, true, false)
}

// Observe is a live activity hint, not an explicit repair. A durable feed may
// already have committed this change; its sequence is checked before rereading.
func (m *Module) Observe(adapterID, sessionID string) {
	if sessionID != "" {
		m.notify(adapterID, sessionID, false, false)
	}
}

func (m *Module) requestRead(adapterID, sessionID string) {
	m.notify(adapterID, sessionID, true, true)
}

func (m *Module) notify(adapterID, sessionID string, force, priority bool) {
	for _, w := range m.workers {
		if adapterID != "" && adapterID != w.source.AdapterID {
			continue
		}
		w.mu.Lock()
		if priority && len(w.priority) < 4096 {
			if w.priority == nil {
				w.priority = map[string]bool{}
			}
			w.priority[sessionID] = true
		}
		if len(w.dirty) >= 4096 {
			w.dirty = map[string]bool{"": true} // bounded queue, repair all on overflow
		} else {
			w.dirty[sessionID] = w.dirty[sessionID] || force
		}
		w.mu.Unlock()
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

func instanceID(owner string, identity platforms.HistoryIdentity) string {
	return statev2.SourceInstanceID(owner, identity.Platform, identity.SourceKey)
}

func (m *Module) report(instanceID, sessionID string, err error) {
	if err != nil && m.deps.OnError != nil {
		m.deps.OnError(instanceID, sessionID, err)
	}
}
