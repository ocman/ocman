package plugins

import (
	"encoding/json"
	"slices"
)

var PageCapability = Capability{Name: "page", Version: Version{Major: 1}}

const PageProjectGrant = "page.project"
const PagePluginGrant = "page.plugin"

// PluginPageCapability allows a plugin-local homepage without a project lookup.
var PluginPageCapability = Capability{Name: "page", Version: Version{Major: 1, Minor: 2}}

// PageModelCapability adds the host's owner/platform model picker.
var PageModelCapability = Capability{Name: "page", Version: Version{Major: 1, Minor: 3}}

// PageTableCapability adds summary cells, severity badges and item drawers.
var PageTableCapability = Capability{Name: "page", Version: Version{Major: 1, Minor: 4}}

// Pages contain bounded typed data only, never executable browser content.
type PageDescriptor struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Validate bool   `json:"validate,omitempty"`
	Scope    string `json:"scope,omitempty"` // empty/project, or plugin
}

type PageRequest struct {
	PageID    string          `json:"pageId"`
	ProjectID string          `json:"projectId"`
	Revision  string          `json:"revision,omitempty"`
	Values    json.RawMessage `json:"values,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	Cursor    string          `json:"cursor,omitempty"`
}

type PageField struct {
	Key         string       `json:"key"`
	Label       string       `json:"label"`
	Type        string       `json:"type"` // text, multiline, secret, boolean, list, projects, model, select, number, integer
	Help        string       `json:"help,omitempty"`
	Fields      []PageField  `json:"fields,omitempty"`
	OptionsFrom string       `json:"optionsFrom,omitempty"`
	Required    bool         `json:"required,omitempty"`
	Minimum     *float64     `json:"minimum,omitempty"`
	Maximum     *float64     `json:"maximum,omitempty"`
	MinLength   *int         `json:"minLength,omitempty"`
	MaxLength   *int         `json:"maxLength,omitempty"`
	MinItems    *int         `json:"minItems,omitempty"`
	MaxItems    *int         `json:"maxItems,omitempty"`
	Options     []PageOption `json:"options,omitempty"`
}

type PageDocument struct {
	Revision   string          `json:"revision"`
	Fields     []PageField     `json:"fields"`
	Values     json.RawMessage `json:"values"`
	Tree       PaneTree        `json:"tree"`
	Configured *bool           `json:"configured,omitempty"`
	Collection *PageCollection `json:"collection,omitempty"`
	Detail     *PageDetail     `json:"detail,omitempty"`
	Issues     []PageIssue     `json:"issues,omitempty"`
}

func (d Description) validatePages() error {
	has := d.PageVersion().Major == 1
	if len(d.Pages) > 8 || !has && len(d.Pages) > 0 || has && (d.Scope != ScopeOwner || len(d.Pages) == 0) {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, p := range d.Pages {
		if !validName(p.ID) || !validText(p.Label) || seen[p.ID] || p.Validate && d.PageVersion().Minor < 1 || !slices.Contains([]string{"", "project", "plugin"}, p.Scope) || p.Scope == "plugin" && d.PageVersion().Minor < 2 || !slices.Contains(d.RequestedGrants, pageGrant(p)) {
			return ErrInvalidMessage
		}
		seen[p.ID] = true
	}
	return nil
}

func (d Description) PageAllowed(id string, grants []string) bool {
	return d.Scope == ScopeOwner && d.PageVersion().Major == 1 && slices.ContainsFunc(d.Pages, func(p PageDescriptor) bool { return p.ID == id && slices.Contains(grants, pageGrant(p)) })
}

func pageGrant(p PageDescriptor) string {
	if p.Scope == "plugin" {
		return PagePluginGrant
	}
	return PageProjectGrant
}

// Missing project identity is admitted only on an explicitly plugin-local page.
func (d Description) PageRequestAllowed(req PageRequest) bool {
	return slices.ContainsFunc(d.Pages, func(p PageDescriptor) bool {
		return p.ID == req.PageID && (req.ProjectID != "" || p.Scope == "plugin")
	})
}

func DecodePageRequest(raw []byte) (PageRequest, error) {
	var p PageRequest
	if decodePaneJSON(raw, &p) != nil || !validName(p.PageID) || (ActionContext{ProjectID: p.ProjectID}).Validate() != nil || len(p.Revision) > 128 || p.ItemID != "" && !validName(p.ItemID) || !validPageCursor(p.Cursor) {
		return p, ErrInvalidMessage
	}
	if len(p.Values) > 0 && !json.Valid(p.Values) {
		return p, ErrInvalidMessage
	}
	return p, nil
}

func DecodePageDocument(raw []byte) (PageDocument, error) {
	// Fields decodes as raw JSON so an absent or null schema is a valid
	// results-only document rather than a rejected plugin response.
	var wire struct {
		Revision   string          `json:"revision"`
		Fields     json.RawMessage `json:"fields"`
		Values     json.RawMessage `json:"values"`
		Tree       PaneTree        `json:"tree"`
		Configured *bool           `json:"configured,omitempty"`
		Collection *PageCollection `json:"collection,omitempty"`
		Detail     *PageDetail     `json:"detail,omitempty"`
		Issues     []PageIssue     `json:"issues,omitempty"`
	}
	if decodePaneJSON(raw, &wire) != nil || len(wire.Revision) > 128 || len(wire.Values) == 0 {
		return PageDocument{}, ErrInvalidMessage
	}
	p := PageDocument{Revision: wire.Revision, Values: wire.Values, Tree: wire.Tree, Configured: wire.Configured, Collection: wire.Collection, Detail: wire.Detail, Issues: wire.Issues}
	if !validPageContent(p) {
		return p, ErrInvalidMessage
	}
	if len(wire.Fields) > 0 && string(wire.Fields) != "null" {
		// Keep the strict recursive schema decode: only the whole schema is
		// optional, not its shape.
		if decodePaneJSON(wire.Fields, &p.Fields) != nil {
			return PageDocument{}, ErrInvalidMessage
		}
	}
	count := 0
	if !validPageFields(p.Fields, 0, &count) {
		return p, ErrInvalidMessage
	}
	var values map[string]any
	if json.Unmarshal(p.Values, &values) != nil || values == nil || !validPageValues(p.Fields, values) {
		return p, ErrInvalidMessage
	}
	// The renderer maps over the schema, so a document never carries null there.
	if p.Fields == nil {
		p.Fields = []PageField{}
	}
	tree, _ := json.Marshal(p.Tree)
	if _, err := DecodePaneTree(tree); err != nil {
		return p, err
	}
	return p, nil
}

func validPageFields(fields []PageField, depth int, count *int) bool {
	if depth > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fields {
		*count++
		if *count > 128 || !settingKey.MatchString(f.Key) || !validText(f.Label) || len(f.Help) > 4096 || seen[f.Key] || !slices.Contains([]string{"text", "multiline", "secret", "boolean", "list", "projects", "model", "select", "number", "integer"}, f.Type) || !validPageRules(f) {
			return false
		}
		if f.Type == "select" {
			if len(f.Options) == 0 && !slices.ContainsFunc(fields, func(source PageField) bool {
				return source.Key == f.OptionsFrom && source.Type == "list" && len(source.Fields) > 0 && source.Fields[0].Type == "text"
			}) {
				return false
			}
		} else if f.OptionsFrom != "" {
			return false
		}
		if f.Type != "list" && len(f.Fields) > 0 || f.Type == "list" && len(f.Fields) == 0 {
			return false
		}
		if f.Type == "list" && !validPageFields(f.Fields, depth+1, count) {
			return false
		}
		seen[f.Key] = true
	}
	return true
}

func validPageValues(fields []PageField, values map[string]any) bool {
	for _, field := range fields {
		value, present := values[field.Key]
		if !present {
			continue
		}
		switch field.Type {
		case "number", "integer":
			if !validPageNumber(value, field.Type == "integer") {
				return false
			}
		case "list":
			rows, ok := value.([]any)
			if !ok {
				return false
			}
			for _, row := range rows {
				object, ok := row.(map[string]any)
				if !ok || !validPageValues(field.Fields, object) {
					return false
				}
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return false
			}
		default:
			if _, ok := value.(string); !ok {
				return false
			}
		}
	}
	return true
}
