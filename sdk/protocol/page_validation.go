package protocol

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func validPageRules(f PageField) bool {
	numeric := f.Type == "number" || f.Type == "integer"
	text := slices.Contains([]string{"text", "multiline", "secret"}, f.Type)
	if (f.Minimum != nil || f.Maximum != nil) && !numeric || (f.MinLength != nil || f.MaxLength != nil) && !text || (f.MinItems != nil || f.MaxItems != nil) && f.Type != "list" {
		return false
	}
	for _, n := range []*float64{f.Minimum, f.Maximum} {
		if n != nil && (math.IsInf(*n, 0) || math.IsNaN(*n)) {
			return false
		}
	}
	if f.Minimum != nil && f.Maximum != nil && *f.Minimum > *f.Maximum {
		return false
	}
	for _, pair := range [][2]*int{{f.MinLength, f.MaxLength}, {f.MinItems, f.MaxItems}} {
		if pair[0] != nil && *pair[0] < 0 || pair[1] != nil && *pair[1] < 0 || pair[0] != nil && pair[1] != nil && *pair[0] > *pair[1] {
			return false
		}
	}
	if len(f.Options) > 128 || len(f.Options) > 0 && (f.Type != "select" || f.OptionsFrom != "") {
		return false
	}
	seen := map[string]bool{}
	for _, o := range f.Options {
		if !validText(o.Value) || !validText(o.Label) || seen[o.Value] {
			return false
		}
		seen[o.Value] = true
	}
	return true
}

func validPageIssue(i PageIssue) bool {
	if len(i.Path) > 1024 || !utf8.ValidString(i.Path) || i.Path != "" && !strings.HasPrefix(i.Path, "/") || len(i.Message) == 0 || len(i.Message) > 4096 || !utf8.ValidString(i.Message) {
		return false
	}
	for n := 0; n < len(i.Path); n++ {
		if i.Path[n] == '~' {
			n++
			if n == len(i.Path) || i.Path[n] != '0' && i.Path[n] != '1' {
				return false
			}
		}
	}
	return true
}

// ValidatePageInput applies host-owned static rules to a submitted object.
// Draft documents may violate constraints; only saves must satisfy them.
func ValidatePageInput(fields []PageField, values map[string]any) []PageIssue {
	issues := []PageIssue{}
	var visit func([]PageField, map[string]any, string)
	add := func(path, message string) {
		if len(issues) < 128 {
			issues = append(issues, PageIssue{Path: path, Message: message})
		}
	}
	visit = func(schema []PageField, object map[string]any, prefix string) {
		for key := range object {
			if !slices.ContainsFunc(schema, func(f PageField) bool { return f.Key == key }) {
				add(prefix, "Unknown field.")
			}
		}
		for _, f := range schema {
			path := prefix + "/" + f.Key
			value, present := object[f.Key]
			if !present || value == nil || value == "" {
				if f.Required {
					add(path, "This field is required.")
				}
				if !present {
					continue
				}
			}
			if !validPageValues([]PageField{f}, map[string]any{f.Key: value}) {
				add(path, "Value has the wrong type.")
				continue
			}
			switch f.Type {
			case "list":
				rows := value.([]any)
				if f.MinItems != nil && len(rows) < *f.MinItems {
					add(path, fmt.Sprintf("Add at least %d items.", *f.MinItems))
				}
				if f.MaxItems != nil && len(rows) > *f.MaxItems {
					add(path, fmt.Sprintf("Use at most %d items.", *f.MaxItems))
				}
				for index, row := range rows {
					visit(f.Fields, row.(map[string]any), path+"/"+strconv.Itoa(index))
				}
			case "number", "integer":
				n := value.(float64)
				if f.Minimum != nil && n < *f.Minimum {
					add(path, fmt.Sprintf("Minimum is %g.", *f.Minimum))
				}
				if f.Maximum != nil && n > *f.Maximum {
					add(path, fmt.Sprintf("Maximum is %g.", *f.Maximum))
				}
			case "select":
				s := value.(string)
				allowed := s == "" && !f.Required
				for _, option := range f.Options {
					allowed = allowed || option.Value == s
				}
				if f.OptionsFrom != "" {
					source := slices.IndexFunc(schema, func(source PageField) bool { return source.Key == f.OptionsFrom })
					if source >= 0 {
						rows, _ := object[f.OptionsFrom].([]any)
						for _, row := range rows {
							if object, ok := row.(map[string]any); ok {
								allowed = allowed || object[schema[source].Fields[0].Key] == s
							}
						}
					}
				}
				if !allowed {
					add(path, "Choose an available option.")
				}
			default:
				if s, ok := value.(string); ok {
					n := utf8.RuneCountInString(s)
					if f.MinLength != nil && n < *f.MinLength {
						add(path, fmt.Sprintf("Use at least %d characters.", *f.MinLength))
					}
					if f.MaxLength != nil && n > *f.MaxLength {
						add(path, fmt.Sprintf("Use at most %d characters.", *f.MaxLength))
					}
				}
			}
		}
	}
	visit(fields, values, "")
	return issues
}

func ValidatePageCall(method string, req PageRequest, version Version) error {
	if version.Minor < 2 && req.ProjectID == "" {
		return ErrInvalidMessage
	}
	if method != "read" && method != "save" && method != "validate" {
		return ErrInvalidMessage
	}
	if version.Minor < 1 && (method == "validate" || req.ItemID != "" || req.Cursor != "") {
		return ErrInvalidMessage
	}
	if method == "read" && len(req.Values) > 0 || method != "read" && req.Cursor != "" {
		return ErrInvalidMessage
	}
	return nil
}
