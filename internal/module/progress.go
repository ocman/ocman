package module

import "context"

// Progress counts completed units, including retained/skipped units. A nil
// total is unknown, not zero. Reporting never changes a job's safety decisions.
type Progress struct {
	Phase     string           `json:"phase"`
	Completed int64            `json:"completed"`
	Total     *int64           `json:"total"`
	Unit      string           `json:"unit,omitempty"`
	Counters  map[string]int64 `json:"counters"`
}

type ProgressReporter interface {
	Report(Progress) error
	Fail(error)
}

type progressKey struct{}

func WithProgressReporter(ctx context.Context, reporter ProgressReporter) context.Context {
	return context.WithValue(ctx, progressKey{}, reporter)
}

func ReportProgress(ctx context.Context, progress Progress) error {
	if reporter, ok := ctx.Value(progressKey{}).(ProgressReporter); ok {
		return reporter.Report(progress)
	}
	return nil
}

// ReportError retains a handled per-item failure in the iteration's receipt.
// Callers can continue independent work without claiming the whole pass succeeded.
func ReportError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if reporter, ok := ctx.Value(progressKey{}).(ProgressReporter); ok {
		reporter.Fail(err)
	}
}
