package forgejo

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// Login is one entry from tea's config.yml. We only carry the fields
// ocman needs (name, URL, host, token); other fields (ssh_host,
// insecure, default, ...) are read by tea but irrelevant here.
type Login struct {
	Name  string
	URL   string
	Host  string
	Token string
}

// teaConfig matches the on-disk YAML produced by `tea login add`.
// Field names are tea's verbatim spelling.
type teaConfig struct {
	Logins []struct {
		Name  string `yaml:"name"`
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"logins"`
}

// TeaLogins reads ~/.config/tea/config.yml (or $XDG_CONFIG_HOME/tea/
// config.yml) and returns all complete logins. Returns an empty slice
// (not an error) when the file is absent — that's the common "tea not
// configured" case and the caller treats it as "no Forgejo upstreams".
func TeaLogins() ([]Login, error) {
	for _, dir := range teaConfigDirs() {
		path := filepath.Join(dir, "tea", "config.yml")
		logins, err := parseTeaConfig(path)
		if err != nil {
			return nil, err
		}
		if len(logins) > 0 {
			return logins, nil
		}
		// Empty / non-existent at this candidate, try the next one.
	}
	return nil, nil
}

// teaConfigDirs returns candidate XDG-style config roots in priority
// order. Matches the convention used by the github package for gh.
func teaConfigDirs() []string {
	var dirs []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, xdg)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config"))
	}
	return dirs
}

// parseTeaConfig reads a single tea config.yml at the given path and
// returns the logins it could fully parse. Incomplete logins (missing
// URL, missing token, unparseable URL) are dropped with a debug log
// rather than treated as a fatal error: a partly-corrupt tea config
// must not break ocman's startup.
//
// A missing file returns (nil, nil) — see TeaLogins.
func parseTeaConfig(path string) ([]Login, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading tea config %s: %w", path, err)
	}

	var cfg teaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing tea config %s: %w", path, err)
	}

	out := make([]Login, 0, len(cfg.Logins))
	for _, l := range cfg.Logins {
		if l.URL == "" || l.Token == "" {
			log.WithField("login", l.Name).Debug("forgejo: skipping incomplete tea login")
			continue
		}
		parsed, err := url.Parse(l.URL)
		if err != nil || parsed.Host == "" {
			log.WithField("login", l.Name).
				WithField("url", l.URL).
				Debug("forgejo: skipping login with unparseable url")
			continue
		}
		out = append(out, Login{
			Name:  l.Name,
			URL:   strings.TrimRight(l.URL, "/"),
			Host:  parsed.Host,
			Token: l.Token,
		})
	}
	return out, nil
}
