package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

// Viewer-consent OAuth apps for link previews. An app comes from Settings,
// stored sealed in state.db, or from the environment. Settings wins, so the
// desktop app (which starts with almost no environment) can override or add
// apps; removing a saved app falls back to the environment's. Client secrets
// are write-only: no response ever carries one.

const previewAppsKey = "preview_apps"

type previewAppKind struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Hosted kinds register one app per host (Forgejo, GitLab).
	Hosted bool `json:"hosted,omitempty"`
	// SecretOptional: a PKCE public client needs no secret.
	SecretOptional bool `json:"secretOptional,omitempty"`
	// Env is the OCMAN_<X>_PREVIEW prefix, or the *_APPS list for hosted kinds.
	Env string `json:"env"`
}

var previewAppKinds = []previewAppKind{
	{Kind: "github", Name: "GitHub", Env: "OCMAN_GITHUB_PREVIEW"},
	{Kind: "forgejo", Name: "Forgejo", Hosted: true, Env: "OCMAN_FORGEJO_PREVIEW_APPS"},
	{Kind: "gitlab", Name: "GitLab", Hosted: true, SecretOptional: true, Env: "OCMAN_GITLAB_PREVIEW_APPS"},
	{Kind: "slack", Name: "Slack", Env: "OCMAN_SLACK_PREVIEW"},
	{Kind: "notion", Name: "Notion", Env: "OCMAN_NOTION_PREVIEW"},
	{Kind: "linear", Name: "Linear", SecretOptional: true, Env: "OCMAN_LINEAR_PREVIEW"},
	{Kind: "jira", Name: "Jira", Env: "OCMAN_JIRA_PREVIEW"},
}

func previewAppKindOf(kind string) (previewAppKind, bool) {
	for _, k := range previewAppKinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return previewAppKind{}, false
}

type previewApp struct {
	Kind         string `json:"kind"`
	Host         string `json:"host,omitempty"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// id is the previewauth provider ID the app registers.
func (a previewApp) id() string {
	if a.Host != "" {
		return a.Kind + ":" + a.Host
	}
	return a.Kind
}

func (a previewApp) validate() error {
	k, ok := previewAppKindOf(a.Kind)
	switch {
	case !ok:
		return errors.New("unknown provider")
	case k.Hosted:
		if u, err := url.Parse("https://" + a.Host); a.Host == "" || err != nil || u.Host != a.Host || u.Hostname() == "" {
			return errors.New("host must be a hostname with an optional port")
		}
	case a.Host != "":
		return errors.New("this provider takes no host")
	}
	if a.ClientID == "" || len(a.ClientID) > 512 {
		return errors.New("client ID must be 1-512 characters")
	}
	if a.ClientSecret == "" && !k.SecretOptional {
		return errors.New("client secret is required")
	}
	if len(a.ClientSecret) > 1024 {
		return errors.New("client secret is too long")
	}
	return nil
}

func envPreviewApps(getenv func(string) string) []previewApp {
	var apps []previewApp
	for _, k := range previewAppKinds {
		if !k.Hosted {
			if id := getenv(k.Env + "_CLIENT_ID"); id != "" {
				apps = append(apps, previewApp{Kind: k.Kind, ClientID: id, ClientSecret: getenv(k.Env + "_CLIENT_SECRET")})
			}
			continue
		}
		// host=client_id[:client_secret][,…]
		for _, entry := range strings.Split(getenv(k.Env), ",") {
			if entry = strings.TrimSpace(entry); entry == "" {
				continue
			}
			host, creds, _ := strings.Cut(entry, "=")
			id, secret, _ := strings.Cut(creds, ":")
			apps = append(apps, previewApp{Kind: k.Kind, Host: strings.ToLower(host), ClientID: id, ClientSecret: secret})
		}
	}
	return apps
}

func (s *Server) storedPreviewApps(ctx context.Context) ([]previewApp, error) {
	v, ok, err := s.stateDB.GetSecretSetting(ctx, previewAppsKey)
	if err != nil || !ok {
		return nil, err
	}
	var apps []previewApp
	return apps, json.Unmarshal([]byte(v), &apps)
}

type configuredPreviewApp struct {
	previewApp
	FromEnv bool // the active app is the environment's
	InEnv   bool // the environment also defines this provider
}

// previewApps is every valid app, saved ones first. The first app for a
// provider ID wins, so Settings overrides the environment.
func (s *Server) previewApps(ctx context.Context) []configuredPreviewApp {
	var out []configuredPreviewApp
	index := map[string]int{}
	add := func(a previewApp, fromEnv bool) {
		if err := a.validate(); err != nil {
			log.WithField("entry", a.id()).WithError(err).Warn("preview auth: ignoring invalid app")
			return
		}
		if i, ok := index[a.id()]; ok {
			out[i].InEnv = out[i].InEnv || fromEnv
			return
		}
		index[a.id()] = len(out)
		out = append(out, configuredPreviewApp{a, fromEnv, fromEnv})
	}
	stored, err := s.storedPreviewApps(ctx)
	if err != nil {
		log.WithError(err).Warn("preview auth: reading saved apps failed")
	}
	for _, a := range stored {
		add(a, false)
	}
	for _, a := range envPreviewApps(os.Getenv) {
		add(a, true)
	}
	return out
}

type previewAppView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Host      string `json:"host,omitempty"`
	ClientID  string `json:"clientId"`
	HasSecret bool   `json:"hasSecret"`
	Source    string `json:"source"` // env | settings
	// InEnv: removing a saved app falls back to the environment's.
	InEnv bool `json:"inEnv"`
}

func (s *Server) writePreviewApps(w http.ResponseWriter, r *http.Request) {
	apps := []previewAppView{}
	for _, a := range s.previewApps(r.Context()) {
		src := "settings"
		if a.FromEnv {
			src = "env"
		}
		apps = append(apps, previewAppView{ID: a.id(), Kind: a.Kind, Host: a.Host, ClientID: a.ClientID, HasSecret: a.ClientSecret != "", Source: src, InEnv: a.InEnv})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"kinds": previewAppKinds, "apps": apps, "callbackUrl": s.publicURL(previewCallback)})
}

func (s *Server) handlePreviewApps(w http.ResponseWriter, r *http.Request) {
	if !s.previewAccessAllowed(r) {
		http.Error(w, errPreviewForbidden.Error(), http.StatusForbidden)
		return
	}
	s.writePreviewApps(w, r)
}

// editPreviewApps applies change to the saved apps, then rebuilds the
// providers.
func (s *Server) editPreviewApps(w http.ResponseWriter, r *http.Request, change func([]previewApp) ([]previewApp, error)) {
	if !s.previewAccessAllowed(r) {
		http.Error(w, errPreviewForbidden.Error(), http.StatusForbidden)
		return
	}
	s.previewAuth.appsMu.Lock()
	defer s.previewAuth.appsMu.Unlock()
	apps, err := s.storedPreviewApps(r.Context())
	if err != nil {
		serverError(w, "reading preview apps", err)
		return
	}
	if apps, err = change(apps); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, _ := json.Marshal(apps)
	if err := s.stateDB.SetSecretSetting(r.Context(), previewAppsKey, string(value)); err != nil {
		serverError(w, "saving preview apps", err)
		return
	}
	s.resetPreviews()
	s.writePreviewApps(w, r)
}

// handleSavePreviewApp adds or replaces one saved app. An empty secret keeps
// the saved one, so the client ID can change without re-entering it; an
// environment secret is never copied into state.db.
func (s *Server) handleSavePreviewApp(w http.ResponseWriter, r *http.Request) {
	var req previewApp
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	req.Host = strings.ToLower(strings.TrimSpace(req.Host))
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)
	s.editPreviewApps(w, r, func(apps []previewApp) ([]previewApp, error) {
		kept := apps[:0]
		for _, a := range apps {
			if a.id() != req.id() {
				kept = append(kept, a)
			} else if req.ClientSecret == "" {
				req.ClientSecret = a.ClientSecret
			}
		}
		if err := req.validate(); err != nil {
			return nil, err
		}
		return append(kept, req), nil
	})
}

func (s *Server) handleRemovePreviewApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	// ponytail: the app's viewer grants stay in state.db and return if the
	// same app is added again; purge them here if that ever surprises anyone.
	s.editPreviewApps(w, r, func(apps []previewApp) ([]previewApp, error) {
		kept := apps[:0]
		for _, a := range apps {
			if a.id() != req.ID {
				kept = append(kept, a)
			}
		}
		return kept, nil
	})
}
