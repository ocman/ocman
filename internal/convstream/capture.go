package convstream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/convevents/v1map"
)

// CaptureEnv names the development-only directory that receives each live
// source connection's raw frames as v1map golden input (design §13.2).
const CaptureEnv = "OCMAN_CAPTURE_EVENTS"

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// capture writes one connection's frames as JSONL; a nil capture is off.
type capture struct {
	f   *os.File
	enc *json.Encoder
}

func openCapture(dir string, key Key) *capture {
	if dir == "" {
		return nil
	}
	name := fmt.Sprintf("%s-%s-%d.v1.jsonl", unsafeName.ReplaceAllString(key.Platform, "_"),
		unsafeName.ReplaceAllString(key.SessionID, "_"), time.Now().UnixNano())
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		log.WithError(err).Warn("convstream: event capture disabled")
		return nil
	}
	c := &capture{f: f, enc: json.NewEncoder(f)}
	if err := c.enc.Encode(map[string]string{"session": key.SessionID}); err != nil {
		c.close()
		return nil
	}
	return c
}

func (c *capture) write(frame v1map.Frame) {
	if c == nil {
		return
	}
	if !json.Valid(frame.Data) {
		raw, _ := json.Marshal(string(frame.Data))
		frame.Data = raw
	}
	_ = c.enc.Encode(frame)
}

func (c *capture) close() {
	if c != nil {
		_ = c.f.Close()
	}
}
