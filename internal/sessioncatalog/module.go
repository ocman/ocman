package sessioncatalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type MetadataHistory interface {
	MetadataPage(context.Context, string, statev2.Key, int) ([]statev2.SessionMetadata, error)
	Metadata(context.Context, statev2.Key) (statev2.SessionMetadata, error)
	ChangeHead(context.Context) (int64, error)
	Changes(context.Context, int64, int) ([]statev2.Change, error)
}

type Deps struct {
	Store   *Store
	History MetadataHistory
	OwnerID string
	// Prepare freezes legacy provenance at startup. New owner-created
	// identities already carry origin and are never reclassified by indexing.
	Prepare  func(context.Context) error
	Classify func(Source, *NativeSession)
	OnError  func(error)
	Interval time.Duration
}

// Module indexes Ocman's durable metadata/outbox, never platform content.
// Restart inventories retain IDs; pending early announcements retry metadata
// independently of new native events and without re-running history imports.
type Module struct {
	deps           Deps
	cursor         int64
	pending        map[statev2.Key]bool
	rebuild        bool
	lastFailure    string
	pendingRetryAt time.Time
	pendingDelay   time.Duration
}

func NewModule(deps Deps) *Module {
	if deps.Interval <= 0 {
		deps.Interval = 250 * time.Millisecond
	}
	return &Module{deps: deps, pending: map[statev2.Key]bool{}}
}

func (*Module) Name() string         { return "session-catalogue" }
func (*Module) Routes(module.Router) {}

func (m *Module) Start(ctx context.Context) error {
	if m.deps.Store == nil || m.deps.History == nil || !nonempty(m.deps.OwnerID) {
		return fmt.Errorf("session catalogue requires a store, metadata history and owner")
	}
	if ctx.Err() != nil {
		return nil
	}
	var err error
	if m.deps.Prepare != nil {
		err = m.deps.Prepare(ctx)
	}
	if err == nil {
		err = m.bootstrap(ctx)
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (m *Module) bootstrap(ctx context.Context) error {
	head, err := m.deps.History.ChangeHead(ctx)
	if err != nil {
		return err
	}
	clear(m.pending)
	m.rebuild = false
	var after statev2.Key
	for {
		rows, err := m.deps.History.MetadataPage(ctx, m.deps.OwnerID, after, 500)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		if err := m.project(ctx, rows); err != nil {
			return err
		}
		after = rows[len(rows)-1].Session.Key
	}
	m.cursor = head
	return nil
}

func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(m.deps.Interval)
	defer ticker.Stop()
	var retryAt time.Time
	delay := 100 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		if !time.Now().Before(retryAt) {
			more, err := m.step(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if err.Error() != m.lastFailure && m.deps.OnError != nil {
					m.deps.OnError(err)
				}
				m.lastFailure = err.Error()
				retryAt = time.Now().Add(delay)
				delay = min(2*delay, 30*time.Second)
			} else {
				m.lastFailure = ""
				delay = 100 * time.Millisecond
				if more {
					continue
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) step(ctx context.Context) (bool, error) {
	retryPending := !time.Now().Before(m.pendingRetryAt)
	if m.rebuild && retryPending {
		if err := m.bootstrap(ctx); err != nil {
			return false, err
		}
		retryPending = false // bootstrap already retried pending metadata
		m.schedulePending()
	}
	changes, err := m.deps.History.Changes(ctx, m.cursor, 500)
	if err != nil {
		return false, err
	}
	keys := make(map[statev2.Key]bool, len(changes)+len(m.pending))
	if retryPending {
		for key := range m.pending {
			keys[key] = true
		}
	}
	for _, change := range changes {
		keys[change.Key] = true
	}
	rows := make([]statev2.SessionMetadata, 0, len(keys))
	for key := range keys {
		row, err := m.deps.History.Metadata(ctx, key)
		if errors.Is(err, statev2.ErrNotFound) {
			delete(m.pending, key)
			continue
		}
		if err != nil {
			return false, err
		}
		rows = append(rows, row)
	}
	if err := m.project(ctx, rows); err != nil {
		return false, err
	}
	if len(changes) > 0 {
		m.cursor = changes[len(changes)-1].Sequence
	}
	if retryPending || m.pendingRetryAt.IsZero() {
		m.schedulePending()
	}
	return len(changes) == 500, nil
}

func (m *Module) schedulePending() {
	if len(m.pending) == 0 && !m.rebuild {
		m.pendingRetryAt, m.pendingDelay = time.Time{}, 0
		return
	}
	if m.pendingDelay == 0 {
		m.pendingDelay = 100 * time.Millisecond
	} else {
		m.pendingDelay = min(2*m.pendingDelay, 30*time.Second)
	}
	m.pendingRetryAt = time.Now().Add(m.pendingDelay)
}

func (m *Module) project(ctx context.Context, metadata []statev2.SessionMetadata) error {
	groups := map[Source][]NativeSession{}
	keys := map[Source]map[string]statev2.Key{}
	for _, row := range metadata {
		if row.Instance.OwnerID != m.deps.OwnerID {
			delete(m.pending, row.Session.Key)
			continue
		}
		source := Source{OwnerID: row.Instance.OwnerID, Platform: row.Instance.Platform, SourceKey: row.Instance.SourceKey}
		if row.Purged {
			err := m.deps.Store.Scrub(ctx, source, row.Session.SessionID)
			if errors.Is(err, ErrCreating) {
				if len(m.pending) >= 4096 {
					m.rebuild = true
				} else {
					m.pending[row.Session.Key] = true
				}
			} else if err != nil {
				return err
			} else {
				delete(m.pending, row.Session.Key)
			}
			continue
		}
		native := NativeSession{ID: row.Session.SessionID, ParentID: row.Session.ParentID, Directory: row.Session.Directory, Title: row.Session.Title, CreatedAt: row.Session.CreatedAt}
		if m.deps.Classify != nil {
			m.deps.Classify(source, &native)
		}
		groups[source] = append(groups[source], native)
		if keys[source] == nil {
			keys[source] = map[string]statev2.Key{}
		}
		keys[source][native.ID] = row.Session.Key
	}
	for source, rows := range groups {
		result, err := m.deps.Store.Reconcile(ctx, source, rows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			key := keys[source][row.ID]
			delete(m.pending, key)
			if result.Deferred == 0 {
				continue
			}
			bound, err := m.deps.Store.FindBinding(ctx, Binding{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: row.ID})
			if errors.Is(err, ErrNotFound) || err == nil && !bound.Purged && (bound.CreationState == Creating || bound.CreationState == Configuring) {
				if len(m.pending) >= 4096 {
					m.rebuild = true
				} else {
					m.pending[key] = true
				}
			} else if err != nil {
				return err
			}
		}
	}
	return nil
}
