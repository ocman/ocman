package codexprovider

import (
	"bufio"
	"crypto/sha256"
	"io"
	"maps"
	"os"
	"strings"
	"sync"

	"github.com/ocman/ocman/internal/db"
)

type rolloutUsageCache struct {
	mu    sync.Mutex
	bytes int64
	clock uint64
	files map[string]cachedRollout
}

type cachedRollout struct {
	info   os.FileInfo
	offset int64
	prefix [32]byte
	usage  map[string]TurnUsage
	used   uint64
}

func (c *rolloutUsageCache) read(path string) (map[string]TurnUsage, error) {
	return c.readMode(path, false)
}

func (c *rolloutUsageCache) readMode(path string, force bool) (map[string]TurnUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		delete(c.files, path)
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	old, known := c.files[path]
	c.clock++
	if !force && known && os.SameFile(old.info, st) && old.info.Size() == st.Size() && old.info.ModTime().Equal(st.ModTime()) {
		old.used = c.clock
		c.files[path] = old
		return maps.Clone(old.usage), nil
	}
	next := cachedRollout{info: st, usage: map[string]TurnUsage{}, used: c.clock}
	if strings.HasSuffix(path, ".zst") {
		next.usage, err = ReadRolloutUsage(path)
		c.bytes += st.Size()
	} else {
		if !force && known && os.SameFile(old.info, st) && old.info.Size() < st.Size() && old.offset > 0 && old.offset == old.info.Size() &&
			old.prefix != [32]byte{} && old.prefix == rolloutFingerprint(f, 0, old.offset) {
			next.usage, next.offset = maps.Clone(old.usage), old.offset
		}
		r := bufio.NewReaderSize(io.NewSectionReader(f, next.offset, st.Size()-next.offset), 64<<10)
		for {
			line, readErr := r.ReadBytes('\n')
			c.bytes += int64(len(line))
			if readErr == io.EOF {
				// Match full reads for a valid final JSON record, but replay
				// unterminated bytes on append instead of checkpointing them.
				addUsageLine(next.usage, line)
				break
			}
			if readErr != nil {
				err = readErr
				break
			}
			addUsageLine(next.usage, line)
			next.offset += int64(len(line))
		}
		next.prefix = rolloutFingerprint(f, 0, next.offset)
	}
	if err != nil {
		return nil, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(st, after) || st.Size() != after.Size() || !st.ModTime().Equal(after.ModTime()) {
		return nil, db.ErrHistoryChanged
	}
	if c.files == nil {
		c.files = map[string]cachedRollout{}
	}
	if len(c.files) >= 64 && !known {
		var oldest string
		used := c.clock
		for path, entry := range c.files {
			if entry.used < used {
				oldest, used = path, entry.used
			}
		}
		delete(c.files, oldest)
	}
	c.files[path] = next
	return maps.Clone(next.usage), nil
}

func rolloutFingerprint(f *os.File, offset, size int64) [32]byte {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, offset, size)); err != nil {
		return [32]byte{}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
