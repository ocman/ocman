package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) AgentOptions(ctx context.Context, directories []string) []string {
	if reader, ok := a.native.(platforms.AgentOptionsReader); ok {
		return reader.AgentOptions(ctx, directories)
	}
	return nil
}
