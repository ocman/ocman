package claudecode

import (
	"context"

	"github.com/ocman/ocman/internal/platforms/claude/provider"
)

func (p *claudePlatform) GenerateSessionTitle(ctx context.Context, directory, prompt string) (string, error) {
	return claudeprovider.SessionTitle(ctx, p.slots, p.cli, directory, prompt)
}
