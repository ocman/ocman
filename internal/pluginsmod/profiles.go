package pluginsmod

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

// Named permission profiles (Settings area): user-managed reusable rule sets
// that resolve to pinned []platforms.PermissionRule for session creation
// (sessionsvc.CreateConfigured). Nothing is seeded; the documented example
// profile "read-only" denies everything that can change state:
//
//	POST /api/permission-profiles
//	{"name":"read-only","rules":[
//	  {"permission":"bash","pattern":"*","action":"deny"},
//	  {"permission":"edit","pattern":"*","action":"deny"},
//	  {"permission":"write","pattern":"*","action":"deny"}]}
type permissionProfileRequest struct {
	Name  string                     `json:"name"`
	Rules []platforms.PermissionRule `json:"rules"`
}

func (m *Module) handlePermissionProfiles(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/permission-profiles"), "/")
	if rest == "" {
		m.handlePermissionProfileCollection(w, r)
		return
	}
	if strings.Contains(rest, "/") {
		http.NotFound(w, r)
		return
	}
	m.handlePermissionProfileItem(w, r, rest)
}

func (m *Module) handlePermissionProfileCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := m.plugins.ListPermissionProfiles(r.Context())
		if err != nil {
			httpx.ServerError(w, "listing permission profiles", err)
			return
		}
		if items == nil {
			items = []PermissionProfile{}
		}
		httpx.WriteJSON(w, items)
	case http.MethodPost:
		profile, ok := readPermissionProfile(w, r)
		if !ok {
			return
		}
		id, err := newPermissionProfileID()
		if err != nil {
			httpx.ServerError(w, "generating permission profile ID", err)
			return
		}
		profile.ID = id
		if err := m.plugins.CreatePermissionProfile(r.Context(), profile); err != nil {
			m.writePermissionProfileError(w, "creating permission profile", err)
			return
		}
		httpx.WriteJSONStatus(w, http.StatusCreated, profile)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *Module) handlePermissionProfileItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		profile, err := m.plugins.GetPermissionProfile(r.Context(), id)
		if err != nil {
			m.writePermissionProfileError(w, "getting permission profile", err)
			return
		}
		httpx.WriteJSON(w, profile)
	case http.MethodPut:
		profile, ok := readPermissionProfile(w, r)
		if !ok {
			return
		}
		profile.ID = id
		if err := m.plugins.UpdatePermissionProfile(r.Context(), profile); err != nil {
			m.writePermissionProfileError(w, "updating permission profile", err)
			return
		}
		httpx.WriteJSON(w, profile)
	case http.MethodDelete:
		if err := m.plugins.DeletePermissionProfile(r.Context(), id); err != nil {
			m.writePermissionProfileError(w, "deleting permission profile", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func readPermissionProfile(w http.ResponseWriter, r *http.Request) (PermissionProfile, bool) {
	var req permissionProfileRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return PermissionProfile{}, false
	}
	name := strings.TrimSpace(req.Name)
	if !plugins.ValidPermissionProfileName(name) {
		http.Error(w, "name must be 1-128 UTF-8 bytes without control characters", http.StatusBadRequest)
		return PermissionProfile{}, false
	}
	if err := validatePermissionRules(req.Rules); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return PermissionProfile{}, false
	}
	rulesJSON, err := json.Marshal(req.Rules)
	if err != nil {
		http.Error(w, "invalid permission rules", http.StatusBadRequest)
		return PermissionProfile{}, false
	}
	return PermissionProfile{Name: name, RulesJSON: string(rulesJSON)}, true
}

// validatePermissionRules checks the rules before they are pinned: a profile
// is only useful if it resolves to a non-empty rule set, and rules with an
// unknown action would fail every session created from them.
func validatePermissionRules(rules []platforms.PermissionRule) error {
	if len(rules) == 0 {
		return errors.New("at least one permission rule is required")
	}
	if len(rules) > 100 {
		return errors.New("too many rules")
	}
	for _, rule := range rules {
		if strings.TrimSpace(rule.Permission) == "" {
			return errors.New("rule permission is required")
		}
		switch rule.Action {
		case "allow", "deny", "ask":
		default:
			return errors.New("invalid rule action: expected allow, deny, or ask")
		}
	}
	return nil
}

func newPermissionProfileID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "profile-" + hex.EncodeToString(raw[:]), nil
}

func (m *Module) writePermissionProfileError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, ErrPermissionProfileNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionProfileNameTaken):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		httpx.ServerError(w, operation, err)
	}
}
