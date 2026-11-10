package local

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ocman/ocman/internal/ocruntime"
)

// NewOpenCodeConnection builds the host-side lifecycle for one named account.
// ManagedStore must be owner/account/kind scoped persistence, not the legacy
// managed_opencode table. The extra root filter prevents cleanup or recovery
// from touching siblings even if the supplied inventory contains other roots.
func NewOpenCodeConnection(deps Deps, root string) (*Host, error) {
	if !filepath.IsAbs(root) || deps.Runtime == nil || deps.ManagedStore == nil {
		return nil, fmt.Errorf("OpenCode connection requires an absolute runtime root, runtime and scoped store")
	}
	deps.OpenCodeRoot = filepath.Clean(root)
	deps.OpenCodeService = nil
	deps.ManagedStore = &openCodeConnectionStore{store: deps.ManagedStore, root: deps.OpenCodeRoot}
	return New(deps), nil
}

func (h *Host) machineRoot() string {
	if h.deps.OpenCodeRoot != "" {
		return h.deps.OpenCodeRoot
	}
	return machineRoot()
}

type openCodeConnectionStore struct {
	store ManagedStore
	root  string
}

func (s *openCodeConnectionStore) Upsert(ctx context.Context, root string, inst ManagedInstance) error {
	if root != s.root || inst.Kind != ocruntime.KindNativeTmux {
		return fmt.Errorf("OpenCode connection runtime ownership mismatch")
	}
	return s.store.Upsert(ctx, root, inst)
}

func (s *openCodeConnectionStore) Get(ctx context.Context, root string) (ManagedInstance, bool, error) {
	if root != s.root {
		return ManagedInstance{}, false, nil
	}
	inst, found, err := s.store.Get(ctx, root)
	if found && inst.Kind != ocruntime.KindNativeTmux {
		return ManagedInstance{}, false, fmt.Errorf("OpenCode connection runtime kind mismatch")
	}
	return inst, found, err
}

func (s *openCodeConnectionStore) Delete(ctx context.Context, root string) error {
	if root != s.root {
		return fmt.Errorf("OpenCode connection runtime ownership mismatch")
	}
	if _, _, err := s.Get(ctx, root); err != nil {
		return err
	}
	return s.store.Delete(ctx, root)
}

func (s *openCodeConnectionStore) List(ctx context.Context) (map[string]ManagedInstance, error) {
	inst, found, err := s.Get(ctx, s.root)
	if err != nil {
		return nil, err
	}
	rows := map[string]ManagedInstance{}
	if found {
		rows[s.root] = inst
	}
	return rows, nil
}
