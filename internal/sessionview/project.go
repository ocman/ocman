// Package sessionview projects normalized public session references into Ocman
// IDs. Native messages, parts and opaque tool/provider payloads are untouched.
package sessionview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Lookup func(context.Context, string, []string) (map[string]string, error)

type Projector struct {
	Lookup   Lookup
	Platform string
	// OmitUnresolvedRows filters incomplete identities from top-level arrays.
	// Shape alone determines which fields are session references.
	OmitUnresolvedRows bool
	Shape              *Shape
	cache              map[key]string
}

type key struct{ platform, native string }
type reference struct {
	key key
	set func(string)
}

func (p *Projector) JSON(ctx context.Context, raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	refs := []reference{}
	shape := p.Shape
	if shape == nil {
		shape = ConversationShape
	}
	collect(value, p.Platform, shape, &refs)
	_, list := value.([]any)
	list = list && p.OmitUnresolvedRows
	if len(refs) == 0 {
		return raw, nil
	}
	if p.cache == nil {
		p.cache = map[key]string{}
	}
	missing := map[string][]string{}
	seen := map[key]bool{}
	for _, ref := range refs {
		if p.cache[ref.key] == "" && !seen[ref.key] {
			missing[ref.key.platform] = append(missing[ref.key.platform], ref.key.native)
			seen[ref.key] = true
		}
	}
	for platform, ids := range missing {
		for start := 0; start < len(ids); start += 500 {
			end := min(start+500, len(ids))
			resolved, err := p.Lookup(ctx, platform, ids[start:end])
			if err != nil {
				return nil, err
			}
			for _, id := range ids[start:end] {
				canonical := resolved[id]
				if canonical == "" && list {
					continue
				}
				if !strings.HasPrefix(canonical, "ocm_") {
					return nil, fmt.Errorf("missing canonical session identity")
				}
				p.cache[key{platform, id}] = canonical
			}
		}
	}
	if list {
		// A creation with an uncertain native outcome has no public binding
		// yet. Omit only that row, never emit its native ID or fail siblings.
		rows := make([]any, 0, len(value.([]any)))
		refs = nil
		for _, row := range value.([]any) {
			var rowRefs []reference
			collect(row, p.Platform, shape, &rowRefs)
			ready := true
			for _, ref := range rowRefs {
				if p.cache[ref.key] == "" {
					ready = false
					break
				}
			}
			if ready {
				rows = append(rows, row)
				refs = append(refs, rowRefs...)
			}
		}
		value = rows
	}
	for _, ref := range refs {
		canonical := p.cache[ref.key]
		if canonical == "" {
			return nil, fmt.Errorf("missing canonical session identity")
		}
		ref.set(canonical)
	}
	if len(p.cache) > 4096 {
		clear(p.cache)
	}
	return json.Marshal(value)
}
