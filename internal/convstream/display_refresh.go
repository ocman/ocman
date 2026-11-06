package convstream

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/convevents/v1map"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/telemetry"
)

const maxDisplayRefreshes = 64

type displayRefresh struct {
	ref            v1map.PartRef
	done, obsolete bool
}

type displayRefreshState struct {
	jobs    map[v1map.PartRef]*displayRefresh
	pending []*displayRefresh
	reading bool
	workers sync.WaitGroup // admitted under feeder.mu; joined after close fences admission
}

func (f *feeder) refreshLocked(ref v1map.PartRef) {
	r := &f.refresh
	if f.closed || r.jobs[ref] != nil || len(r.pending) >= maxDisplayRefreshes {
		return
	}
	if r.jobs == nil {
		r.jobs = map[v1map.PartRef]*displayRefresh{}
	}
	if len(r.jobs) >= 4096 {
		for key, job := range r.jobs {
			if job.done {
				delete(r.jobs, key)
			}
		}
	}
	job := &displayRefresh{ref: ref}
	r.jobs[ref] = job
	r.pending = append(r.pending, job)
	if !r.reading {
		f.startDisplayWorkerLocked()
	}
}

func (f *feeder) startDisplayWorkerLocked() {
	f.refresh.reading = true
	f.refresh.workers.Go(func() {
		module.RunWithRecover("conversation display refresh", func() {
			defer f.finishDisplayWorker()
			f.readDisplays()
		})
	})
}

func (f *feeder) finishDisplayWorker() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh.reading = false
	if !f.closed && f.ctx.Err() == nil && len(f.refresh.pending) > 0 {
		f.startDisplayWorkerLocked()
	}
}

func (f *feeder) takeDisplayBatch() []*displayRefresh {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.ctx.Err() != nil {
		return nil
	}
	jobs := f.refresh.pending
	f.refresh.pending = nil
	return jobs
}

func (f *feeder) readDisplays() {
	for {
		jobs := f.takeDisplayBatch()
		if len(jobs) == 0 {
			return
		}
		f.readDisplayBatch(jobs)
	}
}

func (f *feeder) readDisplayBatch(jobs []*displayRefresh) {
	defer func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, job := range jobs {
			job.done = true
		}
	}()
	ctx, cancel := context.WithTimeout(f.ctx, refreshTimeout)
	defer cancel()
	finish := telemetry.ConversationWork(ctx, telemetry.ConversationDisplay)
	detail, err := f.p.adapter.Session(ctx, f.p.key.SessionID, 1, 0)
	finish(err, int64(len(jobs)))
	func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, job := range jobs {
			if f.closed || f.ctx.Err() != nil || job.obsolete || err != nil || detail == nil {
				continue
			}
			for _, part := range detail.Parts {
				if part.ID == job.ref.PartID && part.MessageID == job.ref.MessageID {
					f.publishDisplayLocked(part)
					break
				}
			}
		}
	}()
}

// A cursorless display read cannot overwrite a later ordered anchor or removal.
func (f *feeder) invalidateDisplayLocked(ev convevents.Event) {
	var ref v1map.PartRef
	message := ""
	all := false
	switch e := ev.(type) {
	case convevents.PartUpserted:
		ref = v1map.PartRef{MessageID: e.Part.MessageID, PartID: e.Part.ID}
	case convevents.PartDelta:
		ref = v1map.PartRef{MessageID: e.MessageID, PartID: e.PartID}
	case convevents.PartRemoved:
		ref = v1map.PartRef{MessageID: e.MessageID, PartID: e.PartID}
	case convevents.MessageRemoved:
		message = e.MessageID
	case convevents.HistoryRewritten:
		all = true
	default:
		return
	}
	if job := f.refresh.jobs[ref]; job != nil {
		job.obsolete = true
	}
	if all || message != "" {
		for key, job := range f.refresh.jobs {
			if all || key.MessageID == message {
				job.obsolete = true
			}
		}
	}
}

func (f *feeder) publishDisplayLocked(part db.Part) {
	var data map[string]json.RawMessage
	if json.Unmarshal(part.Data, &data) != nil || data == nil {
		return
	}
	data["id"], _ = json.Marshal(part.ID)
	data["messageID"], _ = json.Marshal(part.MessageID)
	data["sessionID"], _ = json.Marshal(f.p.key.SessionID)
	raw, err := json.Marshal(map[string]any{"type": "message.part.updated", "properties": map[string]any{"part": data}})
	if err == nil {
		display := v1map.New(v1map.Options{SessionID: f.p.key.SessionID, Now: f.p.now})
		for _, ev := range display.Map(v1map.Frame{Data: raw}).Events {
			f.sink.Emit(ev, Meta{})
		}
	}
}
