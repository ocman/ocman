package sessionview

import (
	"github.com/ocman/ocman/internal/platforms"
	"strings"
)

// Shape is an explicit public wire contract. Only named reference fields and
// children are visited. Unknown fields and provider/tool payloads are opaque,
// even when they happen to contain sessionId, id or parentID keys.
type Shape struct {
	OwnerField string
	References []string
	// ReferenceKeys marks a dictionary whose keys are session references.
	// Its values remain opaque unless Dictionary supplies a value shape.
	ReferenceKeys bool
	Children      map[string]*Shape
	Dictionary    *Shape
	Variants      map[string]*Shape
}

var ReferenceShape = &Shape{References: []string{"sessionId", "sessionID", "session_id", "parentSessionId", "parentSessionID", "ownerSessionIds", "sessionIds", "busySessions"}}
var SessionShape = &Shape{References: []string{"id", "parentId", "parentID", "sessionId"}, Children: map[string]*Shape{"permissions": ReferenceShape, "questions": ReferenceShape}}

var eventShape = &Shape{Variants: map[string]*Shape{
	"stream.hello": {Children: map[string]*Shape{
		"overlay": {Children: map[string]*Shape{
			"prompts":  ReferenceShape,
			"inflight": {Children: map[string]*Shape{"messages": ReferenceShape, "parts": ReferenceShape}},
		}},
	}},
	"message.upserted": {Children: map[string]*Shape{"message": ReferenceShape}},
	"part.upserted":    {Children: map[string]*Shape{"part": ReferenceShape}},
	"prompt.asked":     {Children: map[string]*Shape{"prompt": ReferenceShape}},
	"child.upserted":   {Children: map[string]*Shape{"child": SessionShape}},
	"child.status":     {References: []string{"childId"}},
}}

// ConversationShape covers REST detail, normalized OCE envelopes and global
// session notifications. It never traverses messages' data or parts' data.
var ConversationShape = &Shape{
	References: ReferenceShape.References,
	Children: map[string]*Shape{
		"session": SessionShape, "sessionTree": SessionShape, "patch": SessionShape,
		"messages": ReferenceShape, "parts": ReferenceShape, "prompts": ReferenceShape,
		"permissions": ReferenceShape, "questions": ReferenceShape, "event": eventShape,
	},
}

func collect(value any, platform string, shape *Shape, refs *[]reference) {
	collectOwned(value, platform, "", shape, refs, false)
}

func collectOwned(value any, platform, owner string, shape *Shape, refs *[]reference, all bool) {
	if shape == nil {
		return
	}
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			collectOwned(child, platform, owner, shape, refs, all)
		}
	case map[string]any:
		if shape.ReferenceKeys {
			for name, value := range item {
				if name != "" && (all || !strings.HasPrefix(name, "ocm_")) {
					*refs = append(*refs, reference{key{platform, name}, func(id string) {
						delete(item, name)
						item[id] = value
					}})
				}
			}
		}
		if shape.Dictionary != nil {
			for _, child := range item {
				collectOwned(child, platform, owner, shape.Dictionary, refs, all)
			}
			return
		}
		if len(shape.Variants) > 0 {
			kind, _ := item["type"].(string)
			collectOwned(item, platform, owner, shape.Variants[kind], refs, all)
			return
		}
		if explicit, ok := item["platform"].(string); ok && explicit != "" {
			platform = explicit
		}
		if shape.OwnerField != "" {
			if explicit, ok := item[shape.OwnerField].(string); ok && explicit != "" {
				owner = explicit
			}
		}
		if remote, base := platforms.SplitPlatformID(platform); remote == "" && owner != "" && owner != "local" {
			platform = platforms.CompoundPlatformID(owner, base)
			if item["platform"] != nil {
				item["platform"] = platform
			}
		}
		if root, ok := item["session"].(map[string]any); ok && shape.Children["session"] != nil {
			if explicit, ok := root["platform"].(string); ok && explicit != "" {
				platform = explicit
			}
		}
		for _, name := range shape.References {
			add := func(id string, set func(string)) {
				if id != "" && (all || !strings.HasPrefix(id, "ocm_")) {
					*refs = append(*refs, reference{key{platform, id}, set})
				}
			}
			switch field := item[name].(type) {
			case string:
				add(field, func(id string) { item[name] = id })
			case []any:
				for index, value := range field {
					if id, ok := value.(string); ok {
						add(id, func(id string) { field[index] = id })
					}
				}
			}
		}
		for name, child := range shape.Children {
			collectOwned(item[name], platform, owner, child, refs, all)
		}
	}
}
