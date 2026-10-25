package plugins

import (
	"encoding/json"
	"math"
	"slices"
	"unicode"
	"unicode/utf8"
)

// PageContributionCapability adds bounded collections, details and validation.
var PageContributionCapability = Capability{Name: "page", Version: Version{Major: 1, Minor: 1}}

type PageOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type PageColumn struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Display string `json:"display"` // text, number, boolean, status, summary, severity
}

// PageSummary is a primary label with optional secondary text and an extra-item count.
type PageSummary struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Count       int    `json:"count,omitempty"`
}

type PageRow struct {
	ID        string         `json:"id"`
	Values    map[string]any `json:"values"`
	ProjectID string         `json:"projectId,omitempty"` // 1.2: open a configuration in this project context
}

type PageCollection struct {
	DetailPresentation string       `json:"detailPresentation,omitempty"` // 1.4: empty for navigation, or drawer
	Title              string       `json:"title"`
	Columns            []PageColumn `json:"columns"`
	Rows               []PageRow    `json:"rows"`
	NextCursor         string       `json:"nextCursor,omitempty"`
}

type PageProperty struct {
	Label   string `json:"label"`
	Display string `json:"display"`
	Value   any    `json:"value"`
}

type PageDetail struct {
	Title      string         `json:"title"`
	Properties []PageProperty `json:"properties"`
}

type PageIssue struct {
	Path    string `json:"path"` // JSON Pointer into submitted values; empty means form-wide.
	Message string `json:"message"`
}

func (d Description) PageVersion() Version {
	for _, c := range d.Capabilities {
		if c.Name == "page" && c.Version.Major == 1 {
			return Version{Major: 1, Minor: min(c.Version.Minor, 4)}
		}
	}
	return Version{}
}

func validPageCursor(s string) bool {
	return len(s) <= 1024 && utf8.ValidString(s) && !slices.ContainsFunc([]rune(s), unicode.IsControl)
}

func validPageNumber(v any, integer bool) bool {
	n, ok := v.(float64)
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && (!integer || math.Trunc(n) == n)
}

func validPageDisplay(display string, value any) bool {
	switch display {
	case "summary":
		object, ok := value.(map[string]any)
		if !ok || !validPageDisplay("text", object["label"]) || object["label"] == "" {
			return false
		}
		if description, present := object["description"]; present && !validPageDisplay("text", description) {
			return false
		}
		if count, present := object["count"]; present && (!validPageNumber(count, true) || count.(float64) < 0 || count.(float64) > 1_000_000) {
			return false
		}
		return true
	case "severity":
		s, ok := value.(string)
		return ok && slices.Contains([]string{"critical", "high", "medium", "low", "info", "unknown"}, s)
	case "text":
		s, ok := value.(string)
		return ok && len(s) <= 4096 && utf8.ValidString(s)
	case "number":
		return validPageNumber(value, false)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "status":
		s, ok := value.(string)
		return ok && slices.Contains([]string{"open", "in_progress", "blocked", "deferred", "closed"}, s)
	}
	return false
}

func validPageContent(p PageDocument) bool {
	if len(p.Issues) > 128 {
		return false
	}
	for _, issue := range p.Issues {
		if !validPageIssue(issue) {
			return false
		}
	}
	if c := p.Collection; c != nil {
		if !slices.Contains([]string{"", "drawer"}, c.DetailPresentation) {
			return false
		}
		if !validText(c.Title) || len(c.Columns) == 0 || len(c.Columns) > 16 || c.Rows == nil || len(c.Rows) > 200 || !validPageCursor(c.NextCursor) {
			return false
		}
		keys, ids := map[string]bool{}, map[string]bool{}
		for _, col := range c.Columns {
			if !settingKey.MatchString(col.Key) || !validText(col.Label) || keys[col.Key] || !slices.Contains([]string{"text", "number", "boolean", "status", "summary", "severity"}, col.Display) {
				return false
			}
			keys[col.Key] = true
		}
		for _, row := range c.Rows {
			if !validName(row.ID) || ids[row.ID] || len(row.Values) != len(c.Columns) || (ActionContext{ProjectID: row.ProjectID}).Validate() != nil {
				return false
			}
			ids[row.ID] = true
			for _, col := range c.Columns {
				if !validPageDisplay(col.Display, row.Values[col.Key]) {
					return false
				}
			}
		}
	}
	if d := p.Detail; d != nil {
		if !validText(d.Title) || d.Properties == nil || len(d.Properties) > 64 {
			return false
		}
		for _, prop := range d.Properties {
			if !validText(prop.Label) || !validPageDisplay(prop.Display, prop.Value) {
				return false
			}
		}
	}
	return true
}

// ValidatePageVersion prevents new features leaking onto a 1.0 connection.
func ValidatePageVersion(p PageDocument, version Version) error {
	if version.Minor < 4 && hasTableDisplay(p) {
		return ErrInvalidMessage
	}
	if version.Minor < 3 && hasModelField(p.Fields) {
		return ErrInvalidMessage
	}
	if version.Minor < 2 && p.Collection != nil {
		for _, row := range p.Collection.Rows {
			if row.ProjectID != "" {
				return ErrInvalidMessage
			}
		}
	}
	if version.Minor >= 1 {
		return nil
	}
	if p.Collection != nil || p.Detail != nil || len(p.Issues) > 0 {
		return ErrInvalidMessage
	}
	var check func([]PageField) bool
	check = func(fields []PageField) bool {
		for _, f := range fields {
			if f.Required || f.Minimum != nil || f.Maximum != nil || f.MinLength != nil || f.MaxLength != nil || f.MinItems != nil || f.MaxItems != nil || len(f.Options) > 0 || f.Type == "number" || f.Type == "integer" || !check(f.Fields) {
				return false
			}
		}
		return true
	}
	if !check(p.Fields) {
		return ErrInvalidMessage
	}
	return nil
}

func hasTableDisplay(p PageDocument) bool {
	if p.Collection != nil && p.Collection.DetailPresentation != "" {
		return true
	}
	modern := func(display string) bool { return display == "summary" || display == "severity" }
	if p.Collection != nil && slices.ContainsFunc(p.Collection.Columns, func(c PageColumn) bool { return modern(c.Display) }) {
		return true
	}
	return p.Detail != nil && slices.ContainsFunc(p.Detail.Properties, func(p PageProperty) bool { return modern(p.Display) })
}

func hasModelField(fields []PageField) bool {
	return slices.ContainsFunc(fields, func(f PageField) bool { return f.Type == "model" || hasModelField(f.Fields) })
}

// PageValuesObject decodes the JSON data received on the wire.
func PageValuesObject(raw json.RawMessage) (map[string]any, error) {
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, ErrInvalidMessage
	}
	return values, nil
}
