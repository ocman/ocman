package claudecode

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/platforms"
)

// StorageSizes totals raw transcripts, including nested subagent transcripts,
// without parsing their contents or relying on the disposable index.
func (p *claudePlatform) StorageSizes(ctx context.Context) ([]platforms.StorageSize, error) {
	var size int64
	err := filepath.WalkDir(filepath.Join(p.configDir, "projects"), func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil // absent root or a transcript removed during sampling
		}
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		return nil, err // do not record a misleading partial total
	}
	return []platforms.StorageSize{{Name: "claudecode:raw", SizeBytes: size}}, nil
}
