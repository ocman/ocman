package historyparity

import (
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Deps are the two readers a test comparison runs against.
type Deps struct {
	Source  Source
	Store   *statev2.DB
	OwnerID string
	Now     func() time.Time
}
