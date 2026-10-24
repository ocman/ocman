package analyticsdata

import (
	"slices"
	"sort"
)

type ToolTiming struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type Timing struct {
	AgentMs, ToolMs, UnknownMs int64
	TokensPerSecond            float64
}

func ModelDuration(start, end int64, tools []ToolTiming) int64 {
	if start <= 0 || end <= start {
		return 0
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Start < tools[j].Start })
	cursor, waiting := start, int64(0)
	for _, tool := range tools {
		if tool.Start <= 0 || tool.End <= 0 || tool.End < tool.Start {
			return 0
		}
		from, to := max(start, tool.Start), min(end, tool.End)
		waiting += max(int64(0), to-max(cursor, from))
		cursor = max(cursor, to)
	}
	return end - start - waiting
}

// SummarizeTiming preserves unknown/incomplete timings and excludes the union
// of tool intervals. It is shared with the legacy reader, not a second engine.
func SummarizeTiming(start, duration, output int64, stop string, tools []ToolTiming) Timing {
	r := Timing{UnknownMs: duration}
	if stop == "tool-calls" && len(tools) == 0 {
		return r
	}
	model := ModelDuration(start, start+duration, tools)
	valid := start > 0 && duration > 0
	for _, tool := range tools {
		valid = valid && tool.Start > 0 && tool.End >= tool.Start
	}
	if valid {
		r.AgentMs, r.ToolMs, r.UnknownMs = model, duration-model, 0
	}
	if model > 100 {
		r.TokensPerSecond = float64(output) / (float64(model) / 1000)
	}
	return r
}

// Percentile returns the nearest-rank percentile of values without
// reordering them, or 0 when values is empty.
func Percentile(values []int64, percent int) int64 {
	if len(values) == 0 {
		return 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	return ordered[(len(ordered)*percent+99)/100-1]
}
