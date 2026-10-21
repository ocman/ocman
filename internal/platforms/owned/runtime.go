package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) GenerateSessionTitle(ctx context.Context, directory, prompt string) (string, error) {
	if err := a.checkSource(ctx); err != nil {
		return "", err
	}
	namer, ok := a.native.(platforms.SessionTitleNamer)
	if !ok {
		return "", platforms.ErrUnsupported
	}
	return namer.GenerateSessionTitle(ctx, directory, prompt)
}

func (a *Adapter) ReloadIdle(ctx context.Context) (bool, error) {
	// Runtime idleness remains authoritative before a browsing source exists.
	if reader, ok := a.native.(platforms.ReloadIdleReader); ok {
		return reader.ReloadIdle(ctx)
	}
	if err := a.checkSource(ctx); err != nil {
		return false, err
	}
	// Inspect native inventory here, not the filtered canonical listing: an
	// unpublished creation may already be executing and must block reload.
	rows, err := a.native.Sessions(ctx, "", 0)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if platforms.ReloadSessionActive(row) {
			return false, nil
		}
	}
	return true, nil
}
