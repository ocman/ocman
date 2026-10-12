package share

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// RequestTimeout bounds each relay request.
const RequestTimeout = 30 * time.Second

// NewRelayClient is a relay client with RequestTimeout.
func NewRelayClient(baseURL string) RelayClient {
	return RelayClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: RequestTimeout}}
}

// TooLarge is the error for a share the relay would refuse as too large,
// reported before anything is uploaded.
func TooLarge(message string) error {
	return &RelayError{Op: "uploading", Status: http.StatusRequestEntityTooLarge, Message: message}
}

// WriteRelayError reports *why* publishing to the relay failed.
//
// Sharing is a deliberate user action against a remote service the user
// configured, so a generic 500 is useless here: the fix is almost always
// operational (relay not running, conversation over the size cap, rate
// limited) and only the relay's own answer says which. The full error is
// still logged; the client gets the actionable part.
func WriteRelayError(w http.ResponseWriter, relayURL string, err error) {
	log.WithError(err).WithField("relay_url", relayURL).Error("publishing share to relay")

	var relayErr *RelayError
	if !errors.As(err, &relayErr) {
		http.Error(w, "could not publish the share: "+err.Error(), http.StatusInternalServerError)
		return
	}

	switch {
	case relayErr.Unreachable():
		http.Error(w, fmt.Sprintf("share relay %s is unreachable: %v", relayURL, relayErr.Err), http.StatusBadGateway)
	case relayErr.Status == http.StatusRequestEntityTooLarge:
		http.Error(w, "this conversation is too large for the share relay: "+relayErr.Message, http.StatusRequestEntityTooLarge)
	case relayErr.Status == http.StatusTooManyRequests:
		http.Error(w, "the share relay is rate limiting new shares; try again shortly", http.StatusTooManyRequests)
	default:
		http.Error(w, fmt.Sprintf("share relay %s rejected the request (HTTP %d): %s",
			relayURL, relayErr.Status, relayErr.Message), http.StatusBadGateway)
	}
}
