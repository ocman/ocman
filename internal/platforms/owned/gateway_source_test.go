package owned

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type sourcelessPlatform struct{ platforms.Platform }

func (sourcelessPlatform) ID() platforms.ID { return "plain" }

func TestPhysicalSourceRequiresHistoryIdentity(t *testing.T) {
	if _, err := physicalSource(context.Background(), sourcelessPlatform{}); err == nil {
		t.Fatal("a platform without a session source resolved one")
	}
}
