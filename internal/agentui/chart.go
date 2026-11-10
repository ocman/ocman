package agentui

import (
	"fmt"
	"strings"
	"time"
)

// MaxCharts bounds canvas charts per rendered result; each one is a chart.js instance.
const MaxCharts = 12

var canvasCharts = map[string]bool{"PieChart": true, "MultiBarChart": true, "LineChart": true, "ScatterChart": true}

// validateChart checks the cross-field rules JSON Schema cannot express:
// unique names, series lengths matching their axis, and strictly increasing x.
func validateChart(c map[string]any) error {
	id := c["id"].(string)
	switch c["component"] {
	case "PieChart":
		labels := []string{}
		for _, item := range c["data"].([]any) {
			labels = append(labels, item.(map[string]any)["label"].(string))
		}
		return unique(id, "slice label", labels)
	case "MultiBarChart":
		return validateSeries(id, c["series"].([]any), len(c["categories"].([]any)))
	case "LineChart":
		x := c["x"].(map[string]any)
		values := x["values"].([]any)
		if err := validateSeries(id, c["series"].([]any), len(values)); err != nil {
			return err
		}
		return increasing(id, x["type"].(string), values)
	case "ScatterChart":
		return validateSeries(id, c["series"].([]any), -1)
	}
	return nil
}

// validateSeries requires unique series names and, when length >= 0, values for every x.
func validateSeries(id string, series []any, length int) error {
	names := []string{}
	for _, item := range series {
		s := item.(map[string]any)
		names = append(names, s["name"].(string))
		if values, ok := s["values"].([]any); ok && length >= 0 && len(values) != length {
			return fmt.Errorf("chart %q series %q has %d values, want %d", id, s["name"], len(values), length)
		}
	}
	return unique(id, "series name", names)
}

func unique(id, what string, names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("chart %q repeats %s %q", id, what, name)
		}
		seen[name] = true
	}
	return nil
}

func increasing(id, kind string, values []any) error {
	if kind == "category" {
		return nil
	}
	previous := 0.0
	for i, value := range values {
		var current float64
		if kind == "date" {
			t, err := parseChartDate(value.(string))
			if err != nil {
				return fmt.Errorf("chart %q x value %q is not a valid date", id, value)
			}
			current = float64(t.UnixMilli())
		} else {
			current = value.(float64)
		}
		if i > 0 && current <= previous {
			return fmt.Errorf("chart %q x values must strictly increase", id)
		}
		previous = current
	}
	return nil
}

// parseChartDate parses the catalog's ISO 8601 subset. Times without a zone are UTC.
func parseChartDate(value string) (time.Time, error) {
	if !strings.Contains(value, "T") {
		return time.Parse(time.DateOnly, value)
	}
	if !strings.HasSuffix(value, "Z") && !strings.ContainsAny(value[10:], "+-") {
		value += "Z"
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04Z07:00", value)
}
