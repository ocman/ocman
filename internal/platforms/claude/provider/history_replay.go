package claudeprovider

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ocman/ocman/internal/rawhistory"
)

const replayFormat = "claude-jsonl-v1"

// ReplayHistory runs the normal converter over the archived transcript and
// child links without opening a platform transcript or its index.
func ReplayHistory(sessionID string, archive rawhistory.Snapshot) (*Converter, error) {
	if archive.Format != replayFormat {
		return nil, fmt.Errorf("invalid Claude replay format")
	}
	r, err := gzip.NewReader(bytes.NewReader(archive.GZIP))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	reader := bufio.NewReader(r)
	first, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	c := &Converter{SessionID: sessionID}
	if err := json.Unmarshal(first, &c.Children); err != nil {
		return nil, err
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) > 0 {
			if err := c.AddChecked(line); err != nil {
				return nil, err
			}
		}
		if err == io.EOF {
			break
		}
	}
	c.Finish()
	return c, nil
}
