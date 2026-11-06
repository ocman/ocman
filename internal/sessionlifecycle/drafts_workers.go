package sessionlifecycle

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

func (m *Module) runDraftWorker(ctx context.Context, id string, work func()) {
	module.RunWithRecover("conversation draft", func() {
		defer func() {
			if recover() == nil {
				return
			}
			log.WithField("stack", string(debug.Stack())).Error("conversation setup interrupted by panic")
			write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := (draftStore{m.d.DB}).finish(write, id, nil, "Setup was interrupted. The saved prompt has not been replayed."); err != nil {
				log.WithError(err).Error("persisting interrupted setup")
			}
			m.draftChanged()
		}()
		work()
	})
}
