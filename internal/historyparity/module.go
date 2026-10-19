package historyparity

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

// TaskKind identifies parity runs in the background task list.
const TaskKind = "conversation.history.parity"

// maxLoggedMismatches bounds the warnings per run; the task counters still
// count every mismatched date and model.
const maxLoggedMismatches = 20

type Deps struct {
	Source   Source
	Store    *statev2.DB
	OwnerID  string
	Tasks    tasksvc.Runner // optional; nil runs without a task record
	Delay    time.Duration  // before the first run, letting ingestion catch up
	Interval time.Duration  // between runs
	Now      func() time.Time
}

type Module struct{ deps Deps }

func NewModule(deps Deps) *Module {
	if deps.Delay <= 0 {
		deps.Delay = 10 * time.Minute
	}
	if deps.Interval <= 0 {
		deps.Interval = 24 * time.Hour
	}
	return &Module{deps: deps}
}

func (*Module) Name() string         { return "conversation-parity" }
func (*Module) Routes(module.Router) {}

func (m *Module) Start(context.Context) error {
	if m.deps.Source == nil || m.deps.Store == nil || m.deps.OwnerID == "" {
		return fmt.Errorf("history parity requires an analytics source, a history store and an owner")
	}
	return nil
}

func (m *Module) Run(ctx context.Context) {
	wait := m.deps.Delay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = m.deps.Interval
		if err := m.run(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("comparing analytics with conversation history failed")
		}
	}
}

func (m *Module) run(ctx context.Context) error {
	if m.deps.Tasks == nil {
		_, err := m.compare(ctx, nil)
		return err
	}
	spec := tasksvc.Spec{Kind: TaskKind, Key: "opencode", Scope: "opencode", Title: "Compare analytics with conversation history"}
	return m.deps.Tasks.Do(ctx, spec, func(ctx context.Context, reporter *tasksvc.Reporter) error {
		_, err := m.compare(ctx, reporter.Report)
		return err
	})
}

// Compare runs one comparison with the module's configured readers.
func (m *Module) Compare(ctx context.Context) (Report, error) { return Compare(ctx, m.deps) }

func (m *Module) compare(ctx context.Context, report func(tasksvc.Progress) error) (Report, error) {
	result, err := Compare(ctx, m.deps)
	if err != nil {
		return result, err
	}
	for i, mismatch := range result.Mismatches {
		if i == maxLoggedMismatches {
			log.WithField("remaining", len(result.Mismatches)-i).Warn("more analytics parity mismatches not logged")
			break
		}
		log.WithFields(log.Fields{"kind": mismatch.Kind, "key": mismatch.Key, "field": mismatch.Field,
			"analytics": mismatch.Source, "history": mismatch.History}).Warn("analytics parity mismatch")
	}
	if report == nil {
		return result, nil
	}
	days, models := result.mismatched("day"), result.mismatched("model")
	total := int64(result.Days + result.Models)
	return result, report(tasksvc.Progress{Phase: "compared", Unit: "keys", Completed: total, Total: &total,
		Counters: map[string]int64{
			"days-matched": int64(result.Days - days), "days-mismatched": int64(days),
			"models-matched": int64(result.Models - models), "models-mismatched": int64(models),
		}})
}
