package owned

import (
	"context"
	"maps"

	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) conversationObservations(ctx context.Context) (func(string) *platforms.LiveState, error) {
	observer, ok := a.native.(platforms.CachedSessionObserver)
	if !ok {
		return a.native.LiveStatus, nil
	}
	observations := maps.Clone(observer.CachedSessionObservations())
	if observations == nil {
		observations = map[string]platforms.LiveState{}
	}
	if err := a.backgroundObservations(ctx, observations); err != nil {
		return nil, err
	}
	permissions, questions := map[string]bool{}, map[string]bool{}
	for id, live := range observations {
		if live.PendingPermission {
			permissions[id] = true
		}
		if live.PendingQuestion {
			questions[id] = true
		}
	}
	if len(permissions)+len(questions) > 0 {
		instance, err := a.history.FindInstance(ctx, a.owner, a.source.Platform, a.source.SourceKey)
		if err != nil {
			return nil, err
		}
		parents, err := a.storedNativeParents(ctx, instance)
		if err != nil {
			return nil, err
		}
		for id := range platforms.BubblePromptSessions(permissions, parents) {
			v := observations[id]
			v.PendingPermission = true
			observations[id] = v
		}
		for id := range platforms.BubblePromptSessions(questions, parents) {
			v := observations[id]
			v.PendingQuestion = true
			observations[id] = v
		}
	}
	return func(id string) *platforms.LiveState {
		if v, ok := observations[id]; ok {
			return &v
		}
		return nil
	}, nil
}
