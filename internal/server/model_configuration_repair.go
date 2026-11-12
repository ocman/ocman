package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
)

// Repair only obsolete native-kind preferences. Named accounts, session bindings,
// claimed policies and historical runs are never reassigned by this repair.
func (s *Server) repairModelConfigurations(ctx context.Context) error {
	if s.stateDB == nil || s.registry == nil {
		return nil
	}
	configurations, err := s.stateDB.ModelConfigurations(ctx)
	if err != nil {
		return err
	}
	catalogs := map[string]*platforms.DirectoryCatalog{}
	for _, c := range configurations {
		models := append([]string(nil), c.Models...)
		selected, changed := c.Platform, false
		for i, ref := range models {
			next, scope := s.repairSavedModel(ctx, c.Owner, c.Directory, c.Platform, ref, catalogs)
			if next != ref {
				models[i], changed = next, true
			}
			if scope != "" && legacyModelScope(selected) {
				selected, changed = scope, true
			}
		}
		if !changed {
			continue
		}
		repaired, err := s.stateDB.RepairModelConfiguration(ctx, c, selected, models)
		if err != nil {
			return fmt.Errorf("repairing %s model configuration %s: %w", c.Kind, c.ID, err)
		}
		if repaired {
			log.WithFields(log.Fields{"kind": c.Kind, "id": c.ID, "platform": selected}).Info("repaired legacy model configuration")
		}
	}
	return nil
}

func legacyModelScope(scope string) bool {
	switch scope {
	case "", "opencode", "claudecode", "codex":
		return true
	}
	return false
}

func (s *Server) repairSavedModel(ctx context.Context, owner, directory, savedScope, ref string, catalogs map[string]*platforms.DirectoryCatalog) (string, string) {
	if owner == "" || owner == "local" {
		owner = ""
	}
	prefix, plain := platforms.SplitModel(ref)
	_, savedBase := platforms.SplitPlatformID(savedScope)
	if plain == "" && savedBase == "" {
		return ref, ""
	}
	if prefix != "" && savedBase != "" && string(prefix) != savedBase {
		return ref, ""
	}
	// Preserve explicit named account intent, including a disabled or missing account.
	if !legacyModelScope(savedBase) || !legacyModelScope(string(prefix)) {
		return ref, ""
	}
	kind := string(prefix)
	if kind == "" {
		kind = savedBase
	}
	if kind == "" {
		kind = string(platforms.OpenCodeID)
	}
	legacyKey := kind
	if owner != "" {
		legacyKey = platforms.CompoundPlatformID(owner, kind)
	}
	if _, exists := s.registry.Get(platforms.ID(legacyKey)); exists {
		return ref, ""
	}
	for _, entry := range s.registry.Entries() {
		machine, key := platforms.SplitPlatformID(string(entry.Key))
		p := entry.Platform
		native := p.ID().Base()
		if named, ok := p.(interface{ NativeKind() platforms.ID }); ok {
			native = named.NativeKind()
		}
		if machine != owner || !strings.HasPrefix(key, "cnx_") || string(native) != kind || !p.Available(ctx) {
			continue
		}
		if owner == "" && s.connectionStore != nil {
			c, err := s.connectionStore.Get(ctx, key)
			if err != nil || !c.Enabled || c.Removed {
				continue
			}
		}
		if plain == "" {
			return ref, key
		}
		cacheKey := string(entry.Key) + "\x00" + directory
		catalog, loaded := catalogs[cacheKey]
		if !loaded {
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			catalog, _ = p.DirectoryCatalog(readCtx, platforms.DirectoryCatalogRequest{Directory: directory})
			cancel()
			catalogs[cacheKey] = catalog
		}
		if catalog == nil || catalog.Models == nil {
			continue
		}
		for _, model := range catalog.Models.Models {
			if model.Hidden || catalog.Models.HasProviders && !model.IsAvailable {
				continue
			}
			if model.Provider+"/"+model.Model == plain {
				return key + ":" + plain, key
			}
		}
	}
	return ref, ""
}
