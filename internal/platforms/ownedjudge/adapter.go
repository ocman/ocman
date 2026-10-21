// Package ownedjudge preserves typed judge capabilities around canonical
// adapters without making owned depend on the auto-approve feature module.
package ownedjudge

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
)

type adapter struct {
	*owned.Adapter
	judge autoapprove.Judge
}

// Wrap returns the canonical adapter unchanged unless its owner-local source
// provides a fallback judge. Only that optional capability is retained; callers
// never obtain the native platform or an executable native session handle.
func Wrap(canonical *owned.Adapter, native platforms.Integration) platforms.Platform {
	if canonical == nil {
		return nil
	}
	provider, ok := native.(interface{ PermissionJudge() autoapprove.Judge })
	if !ok {
		return canonical
	}
	judge := provider.PermissionJudge()
	if judge == nil {
		return canonical
	}
	return &adapter{Adapter: canonical, judge: &judgeBridge{adapter: canonical, native: judge}}
}

func (a *adapter) PermissionJudge() autoapprove.Judge { return a.judge }

type judgeBridge struct {
	adapter *owned.Adapter
	native  autoapprove.Judge
}

func (j *judgeBridge) JudgeWithCallback(ctx context.Context, directory, permission string, patterns []string, metadata map[string]any, sections []autoapprove.PromptSection, callback func(string)) autoapprove.JudgeResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var projectionError error
	project := func(id string) string {
		if id == "" {
			return ""
		}
		canonical, err := j.adapter.HelperSessionIdentity(ctx, id)
		if err != nil {
			projectionError = err
			cancel()
			return ""
		}
		return canonical
	}
	result := j.native.JudgeWithCallback(ctx, directory, permission, patterns, metadata, sections, func(id string) {
		canonical := project(id)
		if projectionError == nil && callback != nil {
			callback(canonical)
		}
	})
	result.SessionID = project(result.SessionID)
	if projectionError != nil {
		return autoapprove.JudgeResult{Verdict: "unsafe", EvaluationFailed: true}
	}
	return result
}
