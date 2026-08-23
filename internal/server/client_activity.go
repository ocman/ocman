package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxClientActivityClients = 64
	maxClientActivityScopes  = 32
	minClientActivityTTL     = 5 * time.Second
	maxClientActivityTTL     = 2 * time.Minute
)

type clientActivityLease struct {
	ClientID           string   `json:"clientId"`
	Visible            bool     `json:"visible"`
	Focused            bool     `json:"focused"`
	RecentlyInteracted bool     `json:"recentlyInteracted"`
	Scopes             []string `json:"scopes"`
	TTLMS              int64    `json:"ttlMs"`
}

type clientActivityRecord struct {
	visible   bool
	scopes    []string
	expiresAt time.Time
}

type clientActivityPolicy struct {
	mu      sync.Mutex
	now     func() time.Time
	clients map[string]clientActivityRecord
}

func newClientActivityPolicy(now func() time.Time) *clientActivityPolicy {
	return &clientActivityPolicy{now: now, clients: make(map[string]clientActivityRecord)}
}

func (p *clientActivityPolicy) Update(lease clientActivityLease) error {
	if !validClientActivityID(lease.ClientID) {
		return errors.New("invalid clientId")
	}
	if lease.TTLMS <= 0 {
		return errors.New("ttlMs must be positive")
	}
	if len(lease.Scopes) > maxClientActivityScopes {
		return fmt.Errorf("too many scopes: maximum is %d", maxClientActivityScopes)
	}
	for _, scope := range lease.Scopes {
		if !validClientActivityScope(scope) {
			return fmt.Errorf("invalid scope %q", scope)
		}
	}

	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked(now)
	if _, exists := p.clients[lease.ClientID]; !exists && len(p.clients) >= maxClientActivityClients {
		return fmt.Errorf("too many clients: maximum is %d", maxClientActivityClients)
	}
	ttlMS := max(int64(minClientActivityTTL/time.Millisecond), min(lease.TTLMS, int64(maxClientActivityTTL/time.Millisecond)))
	ttl := time.Duration(ttlMS) * time.Millisecond
	p.clients[lease.ClientID] = clientActivityRecord{
		visible:   lease.Visible,
		scopes:    append([]string(nil), lease.Scopes...),
		expiresAt: now.Add(ttl),
	}
	return nil
}

func (p *clientActivityPolicy) HasDemand(scope string) bool {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked(now)
	for _, client := range p.clients {
		if client.visible {
			for _, leasedScope := range client.scopes {
				if leasedScope == scope {
					return true
				}
			}
		}
	}
	return false
}

func (p *clientActivityPolicy) expireLocked(now time.Time) {
	for id, client := range p.clients {
		if !now.Before(client.expiresAt) {
			delete(p.clients, id)
		}
	}
}

func validClientActivityID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.-", r) {
			continue
		}
		return false
	}
	return true
}

func validClientActivityScope(scope string) bool {
	switch scope {
	case "sessions", "projects", "metrics", "workflows":
		return true
	}
	if len(scope) > 1024 {
		return false
	}
	for _, prefix := range []string{"session:", "git-status:"} {
		if strings.HasPrefix(scope, prefix) {
			suffix := strings.TrimPrefix(scope, prefix)
			if strings.TrimSpace(suffix) == "" {
				return false
			}
			for _, r := range suffix {
				if r < ' ' || r == 0x7f {
					return false
				}
			}
			return true
		}
	}
	return false
}

func (s *Server) handleClientActivity(w http.ResponseWriter, r *http.Request) {
	var lease clientActivityLease
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lease); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.activity.Update(lease); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
