package hostsvc

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// ProjectClone is a confirmed clone URL and an absolute owner-local destination.
type ProjectClone struct {
	URL       string `json:"url"`
	Directory string `json:"directory"`
}

var cloneHost = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.\-:]*$`)
var cloneUser = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.\-]*$`)

// SafeCloneURL accepts only HTTPS and SSH, removing embedded HTTP credentials.
// Local paths and external git helpers must never cross the machine boundary.
func SafeCloneURL(raw string) (string, error) {
	invalid := errors.New("repository URL must use HTTPS or SSH without query parameters")
	if strings.ContainsAny(raw, "\x00\r\n\t \\") {
		return "", invalid
	}
	if !strings.Contains(raw, "://") {
		userHost, path, ok := strings.Cut(raw, ":")
		user, host, hasUser := strings.Cut(userHost, "@")
		if !ok || !hasUser || !cloneUser.MatchString(user) || !cloneHost.MatchString(host) || path == "" || strings.HasPrefix(path, "/") {
			return "", invalid
		}
		raw = "ssh://" + userHost + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || !cloneHost.MatchString(u.Host) || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", invalid
	}
	if u.Scheme == "https" {
		u.User = nil
	} else if u.User != nil {
		if _, password := u.User.Password(); password || !cloneUser.MatchString(u.User.Username()) {
			return "", invalid
		}
	}
	if u.Path == "" || u.Path == "/" {
		return "", invalid
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." || strings.ContainsAny(part, "\x00\r\n\\") {
			return "", invalid
		}
	}
	return u.String(), nil
}

// ProjectCloner is embedded in Host so clone operations always execute on the owner.
type ProjectCloner interface {
	PlanProjectClone(context.Context, ProjectClone) (*ProjectClone, error)
	CloneProject(context.Context, ProjectClone) error
}
