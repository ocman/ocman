package sessioncatalog

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/statev2"
)

type MetadataHistory interface {
	MetadataPage(context.Context, string, statev2.Key, int) ([]statev2.SessionMetadata, error)
}

type Deps struct {
	Store   *Store
	History MetadataHistory
	OwnerID string
	// Prepare freezes legacy provenance before canonical ingestion starts.
	Prepare  func(context.Context) error
	Classify func(Source, *NativeSession)
}

// Module backfills retained metadata before canonical ingestion starts.
// Repeated startup inventories retain identities and repair late parents.
type Module struct{ deps Deps }

func NewModule(deps Deps) *Module { return &Module{deps: deps} }

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
	var after statev2.Key
	for {
		rows, err := m.deps.History.MetadataPage(ctx, m.deps.OwnerID, after, 500)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := m.project(ctx, rows); err != nil {
			return err
		}
		after = rows[len(rows)-1].Session.Key
	}
}

func (m *Module) project(ctx context.Context, metadata []statev2.SessionMetadata) error {
	groups := map[Source][]NativeSession{}
	for _, row := range metadata {
		if row.Instance.OwnerID != m.deps.OwnerID {
			continue
		}
		source := Source{OwnerID: row.Instance.OwnerID, Platform: row.Instance.Platform, SourceKey: row.Instance.SourceKey}
		if IsID(row.Session.SessionID) {
			if err := m.validateCanonicalMetadata(ctx, source, row); err != nil {
				return err
			}
			continue
		}
		native := NativeSession{ID: row.Session.SessionID, ParentID: row.Session.ParentID, Directory: row.Session.Directory, Title: row.Session.Title, CreatedAt: row.Session.CreatedAt}
		if m.deps.Classify != nil {
			m.deps.Classify(source, &native)
		}
		groups[source] = append(groups[source], native)
	}
	for source, rows := range groups {
		if _, err := m.deps.Store.Reconcile(ctx, source, rows); err != nil {
			return err
		}
	}
	return nil
}
