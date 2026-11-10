package agentui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func chartDoc(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(chartExample())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "action")
	return doc
}

// chartComponent returns the example component with id, for in-place edits.
func chartComponent(d map[string]any, id string) map[string]any {
	for _, c := range components(d) {
		if c := c.(map[string]any); c["id"] == id {
			return c
		}
	}
	panic("no component " + id)
}

func addComponent(d map[string]any, c map[string]any) {
	body := messages(d)[1]["updateComponents"].(map[string]any)
	body["components"] = append(body["components"].([]any), c)
	root := chartComponent(d, "root")
	root["children"] = append(root["children"].([]any), c["id"])
}

func TestValidateChartExample(t *testing.T) {
	if err := validateDoc(t, chartDoc(t)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAcceptsChartVariants(t *testing.T) {
	for _, tc := range []struct {
		name      string
		component map[string]any
	}{
		{"bar chart unit and format", map[string]any{"id": "c", "component": "BarChart", "label": "Bars", "unit": "MiB", "format": "compact", "data": []any{map[string]any{"label": "a", "value": 1}}}},
		{"pie variant", map[string]any{"id": "c", "component": "PieChart", "label": "Pie", "variant": "pie", "data": []any{map[string]any{"label": "a", "value": 0}}}},
		{"stacked horizontal bars with gaps", map[string]any{"id": "c", "component": "MultiBarChart", "label": "Bars", "mode": "stacked", "orientation": "horizontal", "xLabel": "App", "yLabel": "Cores",
			"categories": []any{"a", "b"}, "series": []any{map[string]any{"name": "requested", "values": []any{1, nil}}, map[string]any{"name": "used", "values": []any{0.5, 2}}}}},
		{"numeric x with negative values", map[string]any{"id": "c", "component": "LineChart", "label": "Line", "format": "percent",
			"x": map[string]any{"type": "number", "values": []any{-1, 0, 2.5}}, "series": []any{map[string]any{"name": "delta", "values": []any{-5, nil, 3}}}}},
		{"category x", map[string]any{"id": "c", "component": "LineChart", "label": "Line",
			"x": map[string]any{"type": "category", "values": []any{"Mon", "Tue"}}, "series": []any{map[string]any{"name": "a", "values": []any{1, 2}}}}},
		{"datetime x with zones", map[string]any{"id": "c", "component": "LineChart", "label": "Line",
			"x":      map[string]any{"type": "date", "values": []any{"2026-11-01T10:00", "2026-11-01T10:30:15.5Z", "2026-11-01T12:00+01:00", "2026-11-01T12:00:01+01:00"}},
			"series": []any{map[string]any{"name": "a", "values": []any{1, 2, 3, 4}}}}},
		{"year zero leap day", map[string]any{"id": "c", "component": "LineChart", "label": "Line",
			"x":      map[string]any{"type": "date", "values": []any{"0000-02-28", "0000-02-29", "0000-03-01"}},
			"series": []any{map[string]any{"name": "a", "values": []any{1, 2, 3}}}}},
		{"scatter", map[string]any{"id": "c", "component": "ScatterChart", "label": "Scatter", "xLabel": "Requested", "yLabel": "Used", "xUnit": "cores", "unit": "cores",
			"series": []any{map[string]any{"name": "a", "points": []any{[]any{1, 2}, []any{-1, 0}}}}}},
		{"stat", map[string]any{"id": "c", "component": "Stat", "label": "Errors", "value": -3, "format": "percent", "delta": -1.5, "deltaLabel": "vs yesterday", "better": "neither"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := chartDoc(t)
			addComponent(d, tc.component)
			if err := validateDoc(t, d); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateRejectsInvalidCharts(t *testing.T) {
	series := func(d map[string]any, id string) []any { return chartComponent(d, id)["series"].([]any) }
	usage := func(d map[string]any) map[string]any { return series(d, "usage")[0].(map[string]any) }
	dates := func(d map[string]any) []any {
		return chartComponent(d, "usage")["x"].(map[string]any)["values"].([]any)
	}
	slice := func(d map[string]any) map[string]any {
		return chartComponent(d, "cpuUsed")["data"].([]any)[0].(map[string]any)
	}
	for _, tc := range []struct {
		name, want string
		edit       func(map[string]any)
	}{
		{"negative slice", "", func(d map[string]any) { slice(d)["value"] = -1 }},
		{"duplicate slice", "repeats slice label", func(d map[string]any) { slice(d)["label"] = "worker" }},
		{"empty pie", "", func(d map[string]any) { chartComponent(d, "cpuUsed")["data"] = []any{} }},
		{"too many slices", "", func(d map[string]any) {
			data := []any{}
			for i := range 51 {
				data = append(data, map[string]any{"label": fmt.Sprint(i), "value": 1})
			}
			chartComponent(d, "cpuUsed")["data"] = data
		}},
		{"slice color", "", func(d map[string]any) { slice(d)["color"] = "#ff0000" }},
		{"chart style", "", func(d map[string]any) { chartComponent(d, "cpuUsed")["style"] = map[string]any{"fill": "red"} }},
		{"unknown variant", "", func(d map[string]any) { chartComponent(d, "cpuUsed")["variant"] = "radar" }},
		{"multiline unit", "", func(d map[string]any) { chartComponent(d, "cpuUsed")["unit"] = "a\nb" }},
		{"long unit", "", func(d map[string]any) { chartComponent(d, "cpuUsed")["unit"] = strings.Repeat("x", 17) }},
		{"unknown format", "", func(d map[string]any) { chartComponent(d, "cpuUsed")["format"] = "currency" }},
		{"series length mismatch", "has 2 values, want 3", func(d map[string]any) { usage(d)["values"] = []any{1, 2} }},
		{"duplicate series", "repeats series name", func(d map[string]any) { series(d, "usage")[1].(map[string]any)["name"] = "api" }},
		{"too many series", "", func(d map[string]any) {
			items := []any{}
			for i := range 9 {
				items = append(items, map[string]any{"name": fmt.Sprint(i), "values": []any{1, 2, 3}})
			}
			chartComponent(d, "usage")["series"] = items
		}},
		{"too many points", "", func(d map[string]any) {
			x := chartComponent(d, "usage")["x"].(map[string]any)
			x["type"], x["values"] = "number", make([]any, 367)
		}},
		{"decreasing dates", "strictly increase", func(d map[string]any) { dates(d)[2] = "2026-10-01" }},
		{"repeated dates", "strictly increase", func(d map[string]any) { dates(d)[1] = "2026-11-01T00:00Z" }},
		{"impossible date", "not a valid date", func(d map[string]any) { dates(d)[1] = "2026-02-30" }},
		{"impossible time", "not a valid date", func(d map[string]any) { dates(d)[1] = "2026-11-02T25:00" }},
		{"offset hour 24", "", func(d map[string]any) { dates(d)[0] = "2026-11-01T00:00+24:00" }},
		{"negative offset hour 24", "", func(d map[string]any) { dates(d)[0] = "2026-11-01T00:00-24:00" }},
		{"offset minute 60", "", func(d map[string]any) { dates(d)[0] = "2026-11-01T00:00+00:60" }},
		{"negative offset minute 60", "", func(d map[string]any) { dates(d)[0] = "2026-11-01T00:00-00:60" }},
		{"malformed date", "", func(d map[string]any) { dates(d)[1] = "Nov 2" }},
		{"decreasing numbers", "strictly increase", func(d map[string]any) {
			chartComponent(d, "usage")["x"] = map[string]any{"type": "number", "values": []any{3, 2, 1}}
		}},
		{"duplicate categories", "", func(d map[string]any) {
			chartComponent(d, "usage")["x"] = map[string]any{"type": "category", "values": []any{"a", "a", "b"}}
		}},
		{"mixed x type", "", func(d map[string]any) {
			chartComponent(d, "usage")["x"] = map[string]any{"type": "number", "values": []any{"2026-11-01", 2, 3}}
		}},
		{"string value", "", func(d map[string]any) { usage(d)["values"] = []any{"1", 2, 3} }},
		{"negative stacked bar", "", func(d map[string]any) {
			addComponent(d, map[string]any{"id": "bars", "component": "MultiBarChart", "label": "Bars", "categories": []any{"a"}, "series": []any{map[string]any{"name": "x", "values": []any{-1}}}})
		}},
		{"bar length mismatch", "has 2 values, want 1", func(d map[string]any) {
			addComponent(d, map[string]any{"id": "bars", "component": "MultiBarChart", "label": "Bars", "categories": []any{"a"}, "series": []any{map[string]any{"name": "x", "values": []any{1, 2}}}})
		}},
		{"four scatter series", "", func(d map[string]any) {
			items := []any{}
			for i := range 4 {
				items = append(items, map[string]any{"name": fmt.Sprint(i), "points": []any{[]any{1, 2}}})
			}
			addComponent(d, map[string]any{"id": "scatter", "component": "ScatterChart", "label": "Scatter", "series": items})
		}},
		{"duplicate scatter series", "repeats series name", func(d map[string]any) {
			point := []any{[]any{1, 2}}
			addComponent(d, map[string]any{"id": "scatter", "component": "ScatterChart", "label": "Scatter", "series": []any{map[string]any{"name": "a", "points": point}, map[string]any{"name": "a", "points": point}}})
		}},
		{"three-value point", "", func(d map[string]any) {
			addComponent(d, map[string]any{"id": "scatter", "component": "ScatterChart", "label": "Scatter", "series": []any{map[string]any{"name": "a", "points": []any{[]any{1, 2, 3}}}}})
		}},
		{"stat string value", "", func(d map[string]any) { chartComponent(d, "cpu")["value"] = "6.4" }},
		{"stat direction", "", func(d map[string]any) { chartComponent(d, "cpu")["better"] = "sideways" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := chartDoc(t)
			tc.edit(d)
			err := validateDoc(t, d)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestChartRenderLimit(t *testing.T) {
	// The example renders 4 charts; each Card repeating the pies row renders 3 more.
	for _, tc := range []struct {
		cards   int
		wantErr bool
	}{{2, false}, {3, true}} {
		d := chartDoc(t)
		for i := range tc.cards {
			addComponent(d, map[string]any{"id": fmt.Sprintf("again%d", i), "component": "Card", "child": "pies"})
		}
		err := validateDoc(t, d)
		if tc.wantErr != (err != nil) || (err != nil && !strings.Contains(err.Error(), "at most 12")) {
			t.Fatalf("%d repeated rows: got %v", tc.cards, err)
		}
	}
}
