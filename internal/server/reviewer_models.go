package server

import (
	"context"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// A named reviewer choice selects its own local credentials, never another owner.
func (s *Server) reviewerPlatform(fallback func() platforms.Platform) platforms.Platform {
	if s.stateDB == nil || s.registry == nil {
		return fallback()
	}
	ref, _, err := s.stateDB.GetSetting(s.lifetime(), state.JudgeModelSettingKey)
	if err != nil {
		return nil
	}
	scope, _ := platforms.SplitModel(ref)
	if !strings.HasPrefix(string(scope), "cnx_") {
		return fallback()
	}
	p, ok := s.registry.Get(scope)
	if !ok || p.ID() != platforms.OpenCodeID || !isLocalAdapter(p) || !p.Available(s.lifetime()) {
		return nil
	}
	return p
}

func (s *Server) reviewerModelEntries(ctx context.Context) []platforms.SessionModel {
	var models []platforms.SessionModel
	if s.registry == nil {
		return models
	}
	for _, entry := range s.registry.Entries() {
		p := entry.Platform
		if !isLocalAdapter(p) || p.ID() != platforms.OpenCodeID || !p.Available(ctx) {
			continue
		}
		reader, ok := p.(platforms.ModelOptionsReader)
		if !ok {
			continue
		}
		name := platforms.DisplayName(entry.Key)
		if s.connectionStore != nil {
			if c, err := s.connectionStore.Get(ctx, string(entry.Key)); err == nil {
				name = c.Name
			}
		}
		for _, ref := range reader.ModelOptions(ctx) {
			provider, model, found := strings.Cut(ref, "/")
			if found {
				models = append(models, platforms.SessionModel{Platform: string(entry.Key), PlatformName: name, Provider: provider, Model: model, IsAvailable: true})
			}
		}
	}
	return models
}
