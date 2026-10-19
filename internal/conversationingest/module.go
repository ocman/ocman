package conversationingest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

type Source struct {
	AdapterID string // routing identity, separate from physical source/platform type
	Label     string
	Reader    platforms.HistoryReader
}

type Deps struct {
	Store        *statev2.DB
	Tasks        tasksvc.Runner
	OwnerID      string
	Sources      []Source
	PollInterval time.Duration
	// VerifyInterval is how often every session is reread to measure drift
	// that version hints missed. It persists across restarts.
	VerifyInterval time.Duration
	Debounce       time.Duration
	ReadTimeout    time.Duration
	OnError        func(instanceID, sessionID string, err error)
}

// Module maintains the authoritative history store. It never broadcasts historical idle events or
// calls the platform's mutating/live methods. Each source has an independent,
// serialized worker and all workers are joined before Run returns.
type Module struct {
	deps    Deps
	workers []*sourceWorker
	readyMu sync.Mutex
	ready   map[string]readySource
	// receiptMu guards the read-approval broadcast and adapter instances.
	receiptMu sync.Mutex
	receiptCh chan struct{}
	instances map[string]string
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
	m := &Module{deps: deps}
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
	for _, w := range m.workers {
		if adapterID != "" && adapterID != w.source.AdapterID {
			continue
		}
		w.mu.Lock()
		if len(w.dirty) >= 4096 {
			w.dirty = map[string]bool{"": true} // bounded queue, repair all on overflow
		} else {
			w.dirty[sessionID] = true
		}
		w.mu.Unlock()
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

func instanceID(owner string, identity platforms.HistoryIdentity) string {
	hash := sha256.Sum256([]byte(owner + "\x00" + identity.Platform + "\x00" + identity.SourceKey))
	return fmt.Sprintf("source-%x", hash)
}

func (m *Module) report(instanceID, sessionID string, err error) {
	if err != nil && m.deps.OnError != nil {
		m.deps.OnError(instanceID, sessionID, err)
	}
}
