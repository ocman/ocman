package provider

import (
	"encoding/json"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// legacyPatterns reports a supervisor older than providerprotocol.ExactPatternsVersion:
// its prompt patterns are the raw tool arguments, so `echo *` means that
// command, never a glob. The adapter escapes them on arrival, before the
// rules, the judge, the audit or a browser see them, so a surviving older
// supervisor cannot create a broad grant.
func (a *Adapter) legacyPatterns() bool {
	return a.hello.ProtocolVersion < providerprotocol.ExactPatternsVersion
}

// exactPrompt escapes a legacy supervisor's patterns and always lists in
// place. Other prompts are returned unchanged.
func (a *Adapter) exactPrompt(p platforms.LivePrompt) platforms.LivePrompt {
	if !a.legacyPatterns() || p == nil {
		return p
	}
	for _, key := range []string{"patterns", "always"} {
		items, ok := p[key].([]any)
		if !ok {
			continue
		}
		escaped := make([]any, len(items))
		for i, item := range items {
			if s, ok := item.(string); ok {
				item = providerprotocol.ExactPattern(s)
			}
			escaped[i] = item
		}
		p[key] = escaped
	}
	return p
}

// exactEvent is exactPrompt for a permission.asked event.
func (a *Adapter) exactEvent(event providerprotocol.Event) (providerprotocol.Event, error) {
	if !a.legacyPatterns() || event.Type != "permission.asked" {
		return event, nil
	}
	var p platforms.LivePrompt
	if err := json.Unmarshal(event.Properties, &p); err != nil {
		return event, err
	}
	data, err := json.Marshal(a.exactPrompt(p))
	event.Properties = data
	return event, err
}
