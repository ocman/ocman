package opencode

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// Reuse the bounded, concurrency-safe TTL cache for warning receipts only.
// Failed HTTP responses remain uncached and are retried on the next request.
var fetchFailureLogs = newHTTPCache(30 * time.Second)

func logFetchFailure(err error, fields log.Fields, msg string) {
	entry := log.WithFields(fields).WithField("error", err)
	// A singleflight follower has no additional cause to report. The caller
	// that performed the fetch logs the actual error.
	if errors.Is(err, context.Canceled) || errors.Is(err, errFetchFailed) {
		entry.Debug(msg)
		return
	}
	port, _ := fields["port"].(string)
	var session interface{}
	if port == "" {
		session = fields["sessionID"]
	}
	key := fmt.Sprintf("%q|%v|%v|%v|%q", msg, fields["endpoint"], fields["path"], session, err.Error())
	fetchFailureLogs.getOrFetch(port, key, func() ([]byte, bool) {
		entry.Warn(msg)
		return []byte{1}, true
	})
}
