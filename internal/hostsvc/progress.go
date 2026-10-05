package hostsvc

import "context"

// Session start steps reported while a new conversation is created. Steps
// may run in parallel (the instance and the worktree checkout do).
const (
	StepOpencode = "opencode"
	StepWorktree = "worktree"
	StepSession  = "session"
	StepPrompt   = "prompt"
)

// Step states.
const (
	StepActive = "active"
	StepDone   = "done"
	StepError  = "error"
)

// ProgressFunc receives step transitions. It must be safe for concurrent use.
type ProgressFunc func(step, state string)

type progressKey struct{}

// WithProgress attaches fn to ctx. It does not cross the remote gRPC seam;
// the hub reports a remote host's work around the call instead.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ReportProgress forwards to the ctx's ProgressFunc, if any.
func ReportProgress(ctx context.Context, step, state string) {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok && fn != nil {
		fn(step, state)
	}
}

// FinishStep reports step done, or error when err is non-nil.
func FinishStep(ctx context.Context, step string, err error) {
	if err != nil {
		ReportProgress(ctx, step, StepError)
		return
	}
	ReportProgress(ctx, step, StepDone)
}
