package server

import (
	"context"
	"errors"
	"net/http"
	"sort"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// accountReader is a platform that reports its subscription account
// (the provider protocol's GET /account, FR-7).
type accountReader interface {
	Account(ctx context.Context) (*providerprotocol.Account, error)
}

type providerAccount struct {
	Platform    string `json:"platform"`
	DisplayName string `json:"displayName"`
	// Status is ok, signed_out (no login) or unavailable.
	Status string `json:"status"`
	providerprotocol.Account
}

// handleProviderAccounts lists every registered platform that reports an
// account, for Settings. Unsupported platforms are left out.
func (s *Server) handleProviderAccounts(w http.ResponseWriter, r *http.Request) {
	accounts := []providerAccount{}
	for _, p := range s.registry.Platforms() {
		reader, ok := p.(accountReader)
		if !ok {
			continue
		}
		entry := providerAccount{Platform: string(p.ID()), DisplayName: p.DisplayName(), Status: "ok"}
		account, err := reader.Account(r.Context())
		switch {
		case errors.Is(err, platforms.ErrUnsupported):
			continue
		case errors.Is(err, platforms.ErrNotFound):
			entry.Status = "signed_out"
		case err != nil:
			log.WithError(err).WithField("platform", entry.Platform).Warn("reading provider account")
			entry.Status = "unavailable"
		default:
			entry.Account = *account
		}
		if entry.RateLimits == nil {
			entry.RateLimits = []providerprotocol.RateLimit{}
		}
		accounts = append(accounts, entry)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Platform < accounts[j].Platform })
	httpx.WriteJSON(w, map[string]any{"accounts": accounts})
}
