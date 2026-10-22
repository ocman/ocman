// Package historyparity supplies test comparisons between native OpenCode
// accounting and statev2 accounting. It is not an application reader.
package historyparity

import (
	"context"
	"sort"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Source is the subset of the OpenCode analytics reader being compared.
type Source interface {
	HistorySourceKey(ctx context.Context) (string, error)
	GetDailyActivity(ctx context.Context, since int64, modelFilter, dir string) ([]db.DailyActivity, error)
	GetModelUsage(ctx context.Context, since int64, dir string) ([]db.ModelUsage, error)
}

// Mismatch is one differing value. It carries dates, model names and counts,
// never conversation content.
type Mismatch struct {
	Kind    string // "day" or "model"
	Key     string // ISO date or "provider/model"
	Field   string
	Source  int64
	History int64
}

// Report summarizes one comparison.
type Report struct {
	Days       int // completed days compared
	Models     int // model keys compared
	Mismatches []Mismatch
}

// window matches GetDailyActivity's one-year range.
const window = 365

// Compare reads both sides and lists every difference. Days that are still
// changing (today) or only partly inside the window are skipped. Model totals
// cover all time, so a session being written while this runs can differ.
func Compare(ctx context.Context, deps Deps) (Report, error) {
	var report Report
	now := deps.now()
	sourceKey, err := deps.Source.HistorySourceKey(ctx)
	if err != nil {
		return report, err
	}
	instance, err := deps.Store.InstanceBySource(ctx, deps.OwnerID, "opencode", sourceKey)
	if err != nil {
		return report, err
	}
	since := now.AddDate(0, 0, -window).UnixMilli()
	sourceDays, err := deps.Source.GetDailyActivity(ctx, since, "", "")
	if err != nil {
		return report, err
	}
	historyDays, err := deps.Store.DailyActivity(ctx, instance.ID, since)
	if err != nil {
		return report, err
	}
	compareDays(&report, sourceDays, historyDays, now)
	sourceModels, err := deps.Source.GetModelUsage(ctx, 0, "")
	if err != nil {
		return report, err
	}
	historyModels, err := deps.Store.ModelUsage(ctx, instance.ID, 0)
	if err != nil {
		return report, err
	}
	compareModels(&report, sourceModels, historyModels)
	return report, nil
}

func compareDays(report *Report, source []db.DailyActivity, history []statev2.DayActivity, now time.Time) {
	today := now.Format("2006-01-02")
	first := now.AddDate(0, 0, -window).Format("2006-01-02")
	type counts struct{ sessions, messages, users int64 }
	both := map[string]*[2]counts{}
	get := func(day string) *[2]counts {
		if both[day] == nil {
			both[day] = &[2]counts{}
		}
		return both[day]
	}
	for _, d := range source {
		get(d.Date)[0] = counts{int64(d.Sessions), int64(d.Messages), int64(d.UserMessages)}
	}
	for _, d := range history {
		get(d.Day)[1] = counts{d.Sessions, d.Messages, d.UserMessages}
	}
	for _, day := range sortedKeys(both) {
		if day >= today || day <= first {
			continue
		}
		report.Days++
		s, h := both[day][0], both[day][1]
		report.add("day", day, "sessions", s.sessions, h.sessions)
		report.add("day", day, "messages", s.messages, h.messages)
		report.add("day", day, "userMessages", s.users, h.users)
	}
}

func compareModels(report *Report, source []db.ModelUsage, history []statev2.ModelUsageRow) {
	type totals struct{ count, in, out, cacheRead, cacheWrite int64 }
	both := map[string]*[2]totals{}
	get := func(key string) *[2]totals {
		if both[key] == nil {
			both[key] = &[2]totals{}
		}
		return both[key]
	}
	for _, m := range source {
		key := m.Model
		if m.Provider != "" {
			key = m.Provider + "/" + m.Model
		}
		t := get(key)
		t[0] = totals{t[0].count + int64(m.Count), t[0].in + m.TokensIn, t[0].out + m.TokensOut, t[0].cacheRead + m.CacheRead, t[0].cacheWrite + m.CacheWrite}
	}
	for _, m := range history {
		get(m.Key)[1] = totals{m.Count, m.TokensIn, m.TokensOut, m.CacheRead, m.CacheWrite}
	}
	for _, key := range sortedKeys(both) {
		report.Models++
		s, h := both[key][0], both[key][1]
		report.add("model", key, "count", s.count, h.count)
		report.add("model", key, "tokensIn", s.in, h.in)
		report.add("model", key, "tokensOut", s.out, h.out)
		report.add("model", key, "cacheRead", s.cacheRead, h.cacheRead)
		report.add("model", key, "cacheWrite", s.cacheWrite, h.cacheWrite)
	}
}

func (r *Report) add(kind, key, field string, source, history int64) {
	if source != history {
		r.Mismatches = append(r.Mismatches, Mismatch{Kind: kind, Key: key, Field: field, Source: source, History: history})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (deps Deps) now() time.Time {
	if deps.Now != nil {
		return deps.Now()
	}
	return time.Now()
}
