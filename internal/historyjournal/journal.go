// Package historyjournal is the durable hand-off between platform hooks and
// Ocman's conversation store. Writers append one JSON record per line and never
// need Ocman to be running. Ocman consumes complete lines from saved offsets,
// so events that arrived while it was offline are read on the next start.
package historyjournal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Version is the record format. Readers reject other versions per record
// rather than guessing at their meaning.
const Version = 1

// ErrGap means a segment shrank below its consumed offset: it was truncated or
// replaced. The consumer cannot know what it missed and must repair from source.
var ErrGap = errors.New("historyjournal: segment shorter than its consumed offset")

// Record is one platform event. Data is the platform's own payload.
type Record struct {
	V       int             `json:"v"`
	Time    int64           `json:"time"`             // writer clock, ms; diagnostic only, never ordering
	Source  string          `json:"source,omitempty"` // physical source, e.g. a config directory
	Session string          `json:"session"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Entry is a record at [Offset, End) in Segment. Err reports an unusable
// complete line; consumers log it and advance past End instead of stalling.
type Entry struct {
	Segment     string
	Offset, End int64
	Record      Record
	Err         error
}

// Dir is the machine-wide journal for one platform. Hooks write it whether or
// not ocman runs; every ocman store on the machine keeps its own offsets.
func Dir(platform string) (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "ocman", "journal", platform), nil
}

// Append serializes writers and terminates an interrupted tail before writing
// the next record. Readers skip the damaged line without losing the next hook.
func Append(dir, segment string, rec Record) error {
	if err := validSegment(segment); err != nil {
		return err
	}
	if rec.Session == "" || rec.Type == "" {
		return fmt.Errorf("historyjournal: record requires session and type")
	}
	rec.V = Version
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, segment), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		var tail [1]byte
		if _, err := f.ReadAt(tail[:], info.Size()-1); err != nil {
			return err
		}
		if tail[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	_, err = f.Write(append(line, '\n'))
	return errors.Join(err, f.Sync())
}

// Read returns up to limit complete records after each segment's cursor, in
// segment-name order. A missing journal directory is an empty journal.
func Read(dir string, cursors map[string]int64, limit int) ([]Entry, error) {
	segments, err := Segments(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, segment := range segments {
		if len(out) >= limit {
			break
		}
		entries, err := ReadSegment(dir, segment, cursors[segment], limit-len(out))
		if err != nil {
			return out, err
		}
		out = append(out, entries...)
	}
	return out, nil
}

// ReadSegment returns up to limit complete records of one segment after offset.
// A segment pruned since it was listed reads as empty.
func ReadSegment(dir, segment string, offset int64, limit int) ([]Entry, error) {
	if err := validSegment(segment); err != nil {
		return nil, err
	}
	return readSegment(dir, segment, offset, limit)
}

// Segments lists journal segment names in consumption order.
func Segments(dir string) ([]string, error) {
	items, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range items {
		if item.Type().IsRegular() && validSegment(item.Name()) == nil {
			names = append(names, item.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func readSegment(dir, segment string, offset int64, limit int) ([]Entry, error) {
	f, err := os.Open(filepath.Join(dir, segment))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // pruned between listing and reading
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < offset {
		return nil, fmt.Errorf("%w: %s at %d, size %d", ErrGap, segment, offset, info.Size())
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	var out []Entry
	for len(out) < limit {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // an incomplete final line is still being written
		}
		if err != nil {
			return out, err
		}
		entry := Entry{Segment: segment, Offset: offset, End: offset + int64(len(line))}
		offset = entry.End
		entry.Err = json.Unmarshal(line, &entry.Record)
		if entry.Err == nil {
			switch {
			case entry.Record.V != Version:
				entry.Err = fmt.Errorf("historyjournal: unsupported record version %d", entry.Record.V)
			case entry.Record.Session == "" || entry.Record.Type == "":
				entry.Err = fmt.Errorf("historyjournal: record without session or type")
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// Prune removes segments that are fully consumed and have not been written for
// idle. A writer that resumes after pruning simply creates the segment again.
func Prune(dir string, cursors map[string]int64, idle time.Duration) ([]string, error) {
	segments, err := Segments(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, segment := range segments {
		path := filepath.Join(dir, segment)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if cursors[segment] != info.Size() || time.Since(info.ModTime()) < idle {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed = append(removed, segment)
	}
	return removed, nil
}

func validSegment(name string) error {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
		return fmt.Errorf("historyjournal: invalid segment name %q", name)
	}
	return nil
}
