package platforms

import "fmt"

// HistoryDeferred reports a partial inventory containing only resolved, readable
// identities. Consumers may import those rows, but must not infer deletions or
// claim complete-history readiness. Other inventory errors discard all rows.
type HistoryDeferred struct {
	Count int
	Err   error
}

func (e *HistoryDeferred) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d history inventory sources incomplete: %v", e.Count, e.Err)
	}
	return fmt.Sprintf("%d session identities deferred while creation is in progress", e.Count)
}

func (e *HistoryDeferred) Unwrap() error { return e.Err }
