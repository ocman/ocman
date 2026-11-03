package composerattachments

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

// TTL is the age after which uploads are swept.
// ponytail: seven-day age cap; reference counting if users need older uploads.
const TTL = 7 * 24 * time.Hour

const sweepInterval = 24 * time.Hour

// Sweeper is the joined worker that removes expired uploads under Root on
// startup and then daily. It has no routes.
type Sweeper struct {
	root     func() string
	interval time.Duration
}

var _ module.Module = (*Sweeper)(nil)
var _ module.Runner = (*Sweeper)(nil)

func NewSweeper() *Sweeper            { return &Sweeper{root: Root, interval: sweepInterval} }
func (*Sweeper) Name() string         { return "composer-attachment-sweep" }
func (*Sweeper) Routes(module.Router) {}

func (s *Sweeper) Run(ctx context.Context) {
	module.Every(ctx, s.Name(), s.interval, func(ctx context.Context) {
		if ctx.Err() != nil {
			return
		}
		if removed := Sweep(s.root(), TTL); removed > 0 {
			log.WithField("removed", removed).Info("swept expired composer attachments")
		}
	})
}
