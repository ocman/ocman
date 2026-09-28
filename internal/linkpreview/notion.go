package linkpreview

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Notion previews pages shared with the viewer's own public connection
// (OAuth, owner=user: the viewer picks the pages in Notion's consent
// screen). Notion only returns pages shared with that connection, so the
// grant is the whole permission model: a page unshared later answers 404
// (not_found once the cached entry expires) and a removed connection 401
// (the grant is forgotten and the viewer asked to connect again).
//
// Direct links on notion.so / notion.com / app.notion.com and published
// *.notion.site pages resolve by page ID to the API-returned canonical URL.
// A configured ticket identifier (ABC-42) is looked up with POST /v1/search
// across the connection's pages; only a title holding the identifier as a
// whole token counts. Several matches return Choices, none is not_found
// (the rule's own link stays the fallback). No URL is ever invented.
type Notion struct {
	// APIBase overrides https://api.notion.com/v1 (tests).
	APIBase string
}

const (
	notionProvider = "notion"
	notionVersion  = "2026-03-11"
)

func (n Notion) base() string {
	if n.APIBase != "" {
		return n.APIBase
	}
	return "https://api.notion.com/v1"
}

func (n Notion) Provider() string { return notionProvider }

func (n Notion) APIHosts() []string {
	u, _ := url.Parse(n.base())
	return []string{u.Host}
}

var (
	notionPageID = regexp.MustCompile(`(?i)(?:^|-)([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	notionTicket = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.#-]{0,63}$`)
)

func notionHost(host string) bool {
	switch strings.ToLower(host) {
	case "notion.so", "www.notion.so", "notion.com", "www.notion.com", "app.notion.com":
		return true
	}
	return strings.HasSuffix(strings.ToLower(host), ".notion.site")
}

// notionUUID returns the dashed lowercase page ID in s, or "".
func notionUUID(s string) string {
	m := notionPageID.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	h := strings.ToLower(strings.ReplaceAll(m[1], "-", ""))
	if len(h) != 32 {
		return ""
	}
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// ParseURL accepts a page link; a ?p= peek ID wins over the path.
func (n Notion) ParseURL(u *url.URL) (Ref, bool) {
	if u.Scheme != "https" || u.Port() != "" || !notionHost(u.Hostname()) {
		return Ref{}, false
	}
	web := "https://" + strings.ToLower(u.Hostname()) + u.EscapedPath()
	id := ""
	if p := u.Query().Get("p"); p != "" {
		if id = notionUUID(p); id != "" {
			web += "?" + url.Values{"p": {p}}.Encode()
		}
	}
	if id == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		id = notionUUID(parts[len(parts)-1])
	}
	if id == "" {
		return Ref{}, false
	}
	return Ref{Kind: "page", ID: id, URL: web}, true
}

func (n Notion) ParseIdentifier(id string) (Ref, bool) {
	if !notionTicket.MatchString(id) {
		return Ref{}, false
	}
	return Ref{Kind: "ticket", ID: id}, true
}

type notionPage struct {
	Object     string    `json:"object"`
	URL        string    `json:"url"`
	LastEdited time.Time `json:"last_edited_time"`
	InTrash    bool      `json:"in_trash"`
	Archived   bool      `json:"archived"`
	IsArchived bool      `json:"is_archived"`
	Icon       *struct {
		Type  string `json:"type"`
		Emoji string `json:"emoji"`
	} `json:"icon"`
	Properties map[string]struct {
		Type  string `json:"type"`
		Title []struct {
			PlainText string `json:"plain_text"`
		} `json:"title"`
	} `json:"properties"`
}

func (p notionPage) gone() bool { return p.Object != "page" || p.InTrash || p.Archived || p.IsArchived }

func (p notionPage) title() string {
	for _, prop := range p.Properties {
		if prop.Type == "title" {
			var b strings.Builder
			for _, t := range prop.Title {
				b.WriteString(t.PlainText)
			}
			return b.String()
		}
	}
	return ""
}

// canonical is the API-returned page URL, only on a Notion host.
func (p notionPage) canonical() string {
	if u, err := url.Parse(p.URL); err == nil && u.Scheme == "https" && u.User == nil && notionHost(u.Hostname()) {
		return u.String()
	}
	return ""
}

func (p notionPage) preview() Preview {
	title := firstNonEmpty(p.title(), "Untitled")
	// ponytail: only emoji icons render; file/external/custom icons would
	// need a proxied image and fall back to the page glyph.
	if p.Icon != nil && p.Icon.Type == "emoji" && p.Icon.Emoji != "" {
		title = p.Icon.Emoji + " " + title
	}
	return Preview{Ref: Ref{URL: p.canonical()}, Title: title, Icon: "bi-file-earmark-text", UpdatedAt: p.LastEdited}
}

func (n Notion) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	api = api.WithHeader("Notion-Version", notionVersion)
	switch ref.Kind {
	case "page":
		if notionUUID(ref.ID) != ref.ID {
			return Preview{}, ErrUnsafeRequest
		}
		var page notionPage
		if err := api.JSON(ctx, http.MethodGet, n.base(), []string{"pages", ref.ID}, nil, nil, &page); err != nil {
			return Preview{}, err
		}
		if page.gone() {
			return Preview{}, &HTTPError{Status: http.StatusNotFound}
		}
		return page.preview(), nil
	case "ticket":
		return n.lookup(ctx, api, ref.ID)
	}
	return Preview{}, ErrUnsafeRequest
}

// lookup searches the connection's page titles for id as a whole token.
func (n Notion) lookup(ctx context.Context, api *API, id string) (Preview, error) {
	if !notionTicket.MatchString(id) {
		return Preview{}, ErrUnsafeRequest
	}
	body := map[string]any{
		"query":     id,
		"filter":    map[string]string{"property": "object", "value": "page"},
		"page_size": 20,
	}
	var res struct {
		Results []notionPage `json:"results"`
	}
	if err := api.JSON(ctx, http.MethodPost, n.base(), []string{"search"}, nil, body, &res); err != nil {
		return Preview{}, err
	}
	token := regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(id) + `(?:$|[^A-Za-z0-9])`)
	var hits []notionPage
	for _, p := range res.Results {
		if !p.gone() && p.canonical() != "" && token.MatchString(p.title()) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return Preview{}, &HTTPError{Status: http.StatusNotFound}
	case 1:
		return hits[0].preview(), nil
	}
	out := Preview{}
	for _, p := range hits {
		out.Choices = append(out.Choices, Choice{Title: p.preview().Title, URL: p.canonical()})
	}
	return out, nil
}

// NotionOAuth is the public-connection consent flow. Notion's own consent
// screen lets the viewer pick which pages to share; the connection's
// capabilities (read content only) are set on the connection itself.
// apiBase "" is production.
func NotionOAuth(clientID, clientSecret, apiBase string) previewauth.Provider {
	n := Notion{APIBase: apiBase}
	return previewauth.Provider{
		ID: notionProvider, Name: "Notion",
		Notice:   "Only the pages you select in Notion are previewed.",
		AuthURL:  n.base() + "/oauth/authorize",
		TokenURL: n.base() + "/oauth/token", RevokeURL: n.base() + "/oauth/revoke",
		ClientID: clientID, ClientSecret: clientSecret,
		BasicAuth: true, JSONBody: true,
		Headers:     map[string]string{"Notion-Version": notionVersion},
		AuthParams:  map[string]string{"owner": "user"},
		DecodeToken: notionToken,
		Identify: func(_ context.Context, _ *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			ws, _ := tok.Raw["workspace_id"].(string)
			if ws == "" {
				return nil, previewauth.ErrExchange
			}
			name, _ := tok.Raw["workspace_name"].(string)
			var account string
			if o, ok := tok.Raw["owner"].(map[string]any); ok {
				if u, ok := o["user"].(map[string]any); ok {
					account, _ = u["name"].(string)
				}
			}
			return []previewauth.Grant{{WorkspaceID: ws, WorkspaceName: name, AccountName: account}}, nil
		},
		TokenHelp: "Create an internal integration at https://www.notion.so/profile/integrations, then share the pages to preview with it.",
		IdentifyToken: func(ctx context.Context, client *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			hosts := map[string]bool{}
			for _, h := range n.APIHosts() {
				hosts[strings.ToLower(h)] = true
			}
			var me struct {
				Name string `json:"name"`
				Bot  struct {
					WorkspaceID   string `json:"workspace_id"`
					WorkspaceName string `json:"workspace_name"`
				} `json:"bot"`
			}
			api := (&API{client: client, token: tok.AccessToken, hosts: hosts}).WithHeader("Notion-Version", notionVersion)
			if err := api.JSON(ctx, http.MethodGet, n.base(), []string{"users", "me"}, nil, nil, &me); err != nil || me.Bot.WorkspaceID == "" {
				return nil, previewauth.ErrExchange
			}
			return []previewauth.Grant{{WorkspaceID: me.Bot.WorkspaceID, WorkspaceName: me.Bot.WorkspaceName, AccountName: me.Name}}, nil
		},
	}
}

// notionToken maps Notion's OAuth error body ({"code":"invalid_grant"}) to
// a revoked grant.
func notionToken(raw map[string]any) (map[string]any, error) {
	if raw["code"] == "invalid_grant" {
		return nil, previewauth.ErrRevoked
	}
	return raw, nil
}
