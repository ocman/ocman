package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/opencodeskills"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/server"
	log "github.com/sirupsen/logrus"
)

func selectedPlatforms(value, knownNames string) (map[string]bool, error) {
	enabled := map[string]bool{}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if knownPlatforms[name] == nil {
			return nil, fmt.Errorf("unknown platform: %q (known: %s)", name, knownNames)
		}
		enabled[name] = true
	}
	return enabled, nil
}

func (f connectionFactory) buildCollection(ctx context.Context, store *platformconnections.Store) ([]platforms.Integration, []platforms.ID, error) {
	collection, err := store.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	var integrations []platforms.Integration
	var keys []platforms.ID
	for _, c := range collection.Connections {
		if !c.Enabled || f.blockedKinds[c.Kind] {
			continue
		}
		p, err := f.build(ctx, c)
		if err != nil {
			log.WithError(err).WithField("connection", c.ID).Warn("connection unavailable at startup")
			continue
		}
		integrations = append(integrations, p)
		keys = append(keys, platforms.ID(c.ID))
	}
	return integrations, keys, nil
}

func blockedConnectionKinds(ctx context.Context, store *platformconnections.Store) (map[string]bool, []server.StartupIssue, error) {
	collection, err := store.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range collection.Connections {
		if c.Enabled && c.Kind == "opencode" {
			if issue := requireOpenCodeV2(map[string]bool{"opencode": true}); issue != nil {
				return map[string]bool{"opencode": true}, []server.StartupIssue{*issue}, nil
			}
			break
		}
	}
	return nil, nil, nil
}

func installLegacyOpenCodeSkills() {
	for _, name := range []string{"ocman-session-splitting", "ocman-workflows"} {
		if err := opencodeskills.Remove(name); err != nil {
			log.WithError(err).Warn("removing retired ocman skill")
		}
	}
	if err := opencodeskills.Install(embeddedSkills()); err != nil {
		log.WithError(err).Warn("installing embedded ocman skills")
	}
}
