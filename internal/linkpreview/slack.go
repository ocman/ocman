package linkpreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Slack previews message permalinks (/archives/{channel}/p{ts}, optionally
// a thread reply via ?thread_ts=) with the viewer's own Slack *user* token.
//
// Isolation: the token comes from a dedicated preview OAuth app the viewer
// consented to; conversation.v1 bot tokens and grants are never used, and a
// bot token in a token response is refused. Slack enforces the viewer's own
// channel membership. Only public-channel scopes are requested, and a
// channel Slack reports as private, DM or group DM is denied before its
// messages are read, so private conversations never reach a preview. A link
// resolves only with a grant whose workspace URL matches the link's host.
type Slack struct {
	// APIBase overrides https://slack.com/api (tests).
	APIBase string
}

const slackProvider = "slack"

// slackUserScopes is the minimum for a public-channel preview: message text
// (channels:history), channel name and privacy (channels:read), author name
// (users:read). ponytail: private channels/DMs need groups:*, im:*, mpim:*;
// add them only with a reviewed policy for showing private excerpts.
const slackUserScopes = "channels:history,channels:read,users:read"

func (s Slack) base() string {
	if s.APIBase != "" {
		return s.APIBase
	}
	return "https://slack.com/api"
}

func (s Slack) Provider() string { return slackProvider }

func (s Slack) APIHosts() []string {
	u, _ := url.Parse(s.base())
	return []string{u.Host}
}

var (
	slackChannel = regexp.MustCompile(`^[CGD][A-Z0-9]{1,20}$`)
	slackPTS     = regexp.MustCompile(`^p([0-9]{10})([0-9]{6})$`)
	slackTS      = regexp.MustCompile(`^[0-9]{10}\.[0-9]{6}$`)
)

func (s Slack) ParseURL(u *url.URL) (Ref, bool) {
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Scheme != "https" || u.Port() != "" || !strings.HasSuffix(host, ".slack.com") ||
		len(parts) != 3 || parts[0] != "archives" || !slackChannel.MatchString(parts[1]) || !slackPTS.MatchString(parts[2]) {
		return Ref{}, false
	}
	ref := Ref{Kind: "message", ID: parts[1] + "/" + parts[2], URL: "https://" + host + "/archives/" + parts[1] + "/" + parts[2]}
	if th := u.Query().Get("thread_ts"); slackTS.MatchString(th) && th != slackTSOf(parts[2]) {
		ref.Kind = "reply"
		ref.URL += "?" + url.Values{"thread_ts": {th}, "cid": {parts[1]}}.Encode()
	}
	return ref, true
}

func (s Slack) ParseIdentifier(string) (Ref, bool) { return Ref{}, false }

// slackTSOf turns p1700000000123456 into 1700000000.123456.
func slackTSOf(p string) string {
	m := slackPTS.FindStringSubmatch(p)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// slackErrors maps Slack's ok:false codes onto the statuses Service handles:
// 401 forgets the grant, 403 is denied, 404 not found.
var slackErrors = map[string]int{
	"invalid_auth": 401, "not_authed": 401, "token_revoked": 401, "account_inactive": 401,
	"missing_scope": 403, "channel_not_found": 403, "not_in_channel": 403, "access_denied": 403,
	"no_permission": 403, "team_access_not_granted": 403, "ekm_access_denied": 403,
	"message_not_found": 404, "thread_not_found": 404, "user_not_found": 404,
	"ratelimited": 429,
}

// call runs a Web API method and decodes an ok:true response into out.
func (s Slack) call(ctx context.Context, api *API, method string, q url.Values, out any) error {
	var raw json.RawMessage
	if err := api.JSON(ctx, http.MethodGet, s.base(), []string{method}, q, nil, &raw); err != nil {
		return err
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if !env.OK {
		if st, ok := slackErrors[env.Error]; ok {
			return &HTTPError{Status: st}
		}
		return errors.New("slack: " + env.Error)
	}
	return json.Unmarshal(raw, out)
}

type slackIdentity struct {
	URL    string `json:"url"`
	Team   string `json:"team"`
	TeamID string `json:"team_id"`
	User   string `json:"user"`
}

type slackMessage struct {
	TS         string `json:"ts"`
	Text       string `json:"text"`
	User       string `json:"user"`
	Username   string `json:"username"`
	ReplyCount int    `json:"reply_count"`
	BotProfile struct {
		Name string `json:"name"`
	} `json:"bot_profile"`
}

func (s Slack) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	u, err := url.Parse(ref.URL)
	channel, p, _ := strings.Cut(ref.ID, "/")
	ts := slackTSOf(p)
	if err != nil || ts == "" || !slackChannel.MatchString(channel) {
		return Preview{}, ErrUnsafeRequest
	}
	var id slackIdentity
	if err := s.call(ctx, api, "auth.test", nil, &id); err != nil {
		return Preview{}, err
	}
	if wu, err := url.Parse(id.URL); err != nil || !strings.EqualFold(wu.Hostname(), u.Hostname()) {
		// ponytail: Enterprise Grid org-level hosts differ from the workspace URL; map them when needed.
		return Preview{}, &HTTPError{Status: http.StatusNotFound}
	}
	var info struct {
		Channel struct {
			Name      string `json:"name"`
			IsPrivate bool   `json:"is_private"`
			IsIM      bool   `json:"is_im"`
			IsMPIM    bool   `json:"is_mpim"`
		} `json:"channel"`
	}
	if err := s.call(ctx, api, "conversations.info", url.Values{"channel": {channel}}, &info); err != nil {
		return Preview{}, err
	}
	if c := info.Channel; c.IsPrivate || c.IsIM || c.IsMPIM {
		return Preview{}, &HTTPError{Status: http.StatusForbidden}
	}
	q := url.Values{"channel": {channel}, "oldest": {ts}, "latest": {ts}, "inclusive": {"true"}, "limit": {"2"}}
	method, status := "conversations.history", ""
	if ref.Kind == "reply" {
		method, status = "conversations.replies", "Thread reply"
		q.Set("ts", u.Query().Get("thread_ts"))
	}
	var hist struct {
		Messages []slackMessage `json:"messages"`
	}
	if err := s.call(ctx, api, method, q, &hist); err != nil {
		return Preview{}, err
	}
	var msg *slackMessage
	for i := range hist.Messages {
		if hist.Messages[i].TS == ts {
			msg = &hist.Messages[i]
		}
	}
	if msg == nil {
		return Preview{}, &HTTPError{Status: http.StatusNotFound}
	}
	author := msg.Username
	if author == "" {
		author = msg.BotProfile.Name
	}
	if msg.User != "" {
		var who struct {
			User struct {
				Name    string `json:"name"`
				Profile struct {
					DisplayName string `json:"display_name"`
					RealName    string `json:"real_name"`
				} `json:"profile"`
			} `json:"user"`
		}
		if s.call(ctx, api, "users.info", url.Values{"user": {msg.User}}, &who) == nil {
			author = firstNonEmpty(who.User.Profile.DisplayName, who.User.Profile.RealName, who.User.Name)
		}
	}
	meta := []string{"#" + info.Channel.Name, author}
	if msg.ReplyCount > 0 {
		meta = append(meta, strconv.Itoa(msg.ReplyCount)+" replies")
	}
	sec, _ := strconv.ParseInt(ts[:10], 10, 64)
	return Preview{
		Ref: Ref{URL: ref.URL}, Title: slackText(msg.Text), Status: status, Icon: "bi-slack",
		Meta: meta, UpdatedAt: time.Unix(sec, 0).UTC(),
	}, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

var (
	slackLabeled = regexp.MustCompile(`<[^<>|]*\|([^<>]*)>`)
	slackBare    = regexp.MustCompile(`<([^<>]*)>`)
)

// slackText renders mrkdwn links/mentions (<url|label>, <@U1>) as plain text.
func slackText(s string) string {
	s = slackBare.ReplaceAllString(slackLabeled.ReplaceAllString(s, "$1"), "$1")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

// SlackOAuth is the viewer-consent app for Slack previews: user-token
// scopes only (user_scope), never a bot token. apiBase "" is production.
func SlackOAuth(clientID, clientSecret, apiBase string) previewauth.Provider {
	s := Slack{APIBase: apiBase}
	return previewauth.Provider{
		ID: slackProvider, Name: "Slack",
		AuthURL:  "https://slack.com/oauth/v2/authorize",
		TokenURL: s.base() + "/oauth.v2.access", RevokeURL: s.base() + "/auth.revoke",
		ClientID: clientID, ClientSecret: clientSecret,
		AuthParams:  map[string]string{"user_scope": slackUserScopes},
		DecodeToken: slackToken,
		Identify: func(ctx context.Context, client *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			hosts := map[string]bool{}
			for _, h := range s.APIHosts() {
				hosts[strings.ToLower(h)] = true
			}
			var id slackIdentity
			if err := s.call(ctx, &API{client: client, token: tok.AccessToken, hosts: hosts}, "auth.test", nil, &id); err != nil || id.TeamID == "" {
				return nil, previewauth.ErrExchange
			}
			return []previewauth.Grant{{WorkspaceID: id.TeamID, WorkspaceName: id.Team, AccountName: id.User}}, nil
		},
	}
}

// slackToken picks the user token from oauth.v2.access: authed_user on the
// authorization-code exchange, top level on a rotation refresh. Bot tokens
// are refused so a preview can never read with bot membership.
func slackToken(raw map[string]any) (map[string]any, error) {
	if ok, _ := raw["ok"].(bool); !ok {
		switch raw["error"] {
		case "invalid_refresh_token", "invalid_grant", "token_revoked":
			return nil, previewauth.ErrRevoked
		}
		return nil, previewauth.ErrExchange
	}
	fields := raw
	if u, ok := raw["authed_user"].(map[string]any); ok && u["access_token"] != nil {
		fields = u
	}
	tok, _ := fields["access_token"].(string)
	if fields["token_type"] == "bot" || strings.HasPrefix(tok, "xoxb-") {
		return nil, previewauth.ErrExchange
	}
	return fields, nil
}
