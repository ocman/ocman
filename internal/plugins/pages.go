package plugins

import (
	"encoding/json"
	"slices"
)

var PageCapability = Capability{Name: "page", Version: Version{Major: 1}}

const PageProjectGrant = "page.project"

// Pages contain bounded typed data only, never executable browser content.
type PageDescriptor struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type PageRequest struct {
	PageID    string          `json:"pageId"`
	ProjectID string          `json:"projectId"`
	Revision  string          `json:"revision,omitempty"`
	Values    json.RawMessage `json:"values,omitempty"`
}

type PageField struct {
	Key    string      `json:"key"`
	Label  string      `json:"label"`
	Type   string      `json:"type"` // text, multiline, secret, boolean, list
	Help   string      `json:"help,omitempty"`
	Fields []PageField `json:"fields,omitempty"`
}

type PageDocument struct {
	Revision string          `json:"revision"`
	Fields   []PageField     `json:"fields"`
	Values   json.RawMessage `json:"values"`
	Tree     PaneTree        `json:"tree"`
}

func (d Description) validatePages() error {
	has := slices.Contains(d.Capabilities, PageCapability)
	if len(d.Pages) > 8 || !has && len(d.Pages) > 0 || has && (d.Scope != ScopeOwner || len(d.Pages) == 0 || !slices.Contains(d.RequestedGrants, PageProjectGrant)) {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, p := range d.Pages {
		if !validName(p.ID) || !validText(p.Label) || seen[p.ID] {
			return ErrInvalidMessage
		}
		seen[p.ID] = true
	}
	return nil
}

func (d Description) PageAllowed(id string, grants []string) bool {
	return d.Scope == ScopeOwner && slices.Contains(d.Capabilities, PageCapability) && slices.Contains(grants, PageProjectGrant) && slices.ContainsFunc(d.Pages, func(p PageDescriptor) bool { return p.ID == id })
}

func DecodePageRequest(raw []byte) (PageRequest, error) {
	var p PageRequest
	if decodePaneJSON(raw, &p) != nil || !validName(p.PageID) || p.ProjectID == "" || (ActionContext{ProjectID: p.ProjectID}).Validate() != nil || len(p.Revision) > 128 {
		return p, ErrInvalidMessage
	}
	if len(p.Values) > 0 && !json.Valid(p.Values) {
		return p, ErrInvalidMessage
	}
	return p, nil
}

func DecodePageDocument(raw []byte) (PageDocument, error) {
	var p PageDocument
	if decodePaneJSON(raw, &p) != nil || len(p.Revision) > 128 || len(p.Values) == 0 {
		return p, ErrInvalidMessage
	}
	count := 0
	if !validPageFields(p.Fields, 0, &count) {
		return p, ErrInvalidMessage
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(p.Values, &values) != nil || values == nil {
		return p, ErrInvalidMessage
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
		if *count > 128 || !settingKey.MatchString(f.Key) || !validText(f.Label) || len(f.Help) > 4096 || seen[f.Key] || !slices.Contains([]string{"text", "multiline", "secret", "boolean", "list"}, f.Type) {
			return false
		}
		if f.Type != "list" && len(f.Fields) > 0 || f.Type == "list" && len(f.Fields) == 0 {
			return false
		}
		if !validPageFields(f.Fields, depth+1, count) {
			return false
		}
		seen[f.Key] = true
	}
	return true
}
