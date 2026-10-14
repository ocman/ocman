package claudecode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
)

func (p *claudePlatform) GenerateSessionTitle(ctx context.Context, directory, prompt string) (string, error) {
	return claudeprovider.SessionTitle(ctx, p.slots, p.cli, directory, prompt)
}
