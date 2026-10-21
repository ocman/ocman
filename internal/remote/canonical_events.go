package remote

import (
	"context"
	"errors"
	"io"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
)

func (p *remotePlatform) ProxyEvents(ctx context.Context, id string, out io.Writer, flush func()) error {
	if !p.CanonicalSessionIDs() {
		return p.proxySessionEvents(ctx, id, out, flush)
	}
	if err := validateCanonicalReferences(id, "", id != ""); err != nil {
		return err
	}
	writer := owned.CanonicalEventValidator(ctx, out)
	if err := p.proxySessionEvents(ctx, id, writer, flush); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.Join(platforms.ErrPlatformUnreachable, err)
	}
	if err := writer.Close(); err != nil {
		return errors.Join(platforms.ErrPlatformUnreachable, err)
	}
	return nil
}
