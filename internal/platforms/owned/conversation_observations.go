package owned

import (
	"context"
	"maps"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) conversationObservations(ctx context.Context) (func(string) *platforms.LiveState, error) {
	observer, ok := a.native.(platforms.CachedSessionObserver)
	if !ok {
		return a.cachedObservationLookup(), nil
	}
	observations := maps.Clone(observer.CachedSessionObservations())
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
