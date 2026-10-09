package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

func (s *Server) handleUIUsage(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Intervals []state.UIUsageInterval `json:"intervals"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid usage payload", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid usage payload", http.StatusBadRequest)
		return
	}
	now := time.Now()
	// Validate here as well as in the store so storage errors remain 500s.
	if len(body.Intervals) > 64 {
		http.Error(w, "too many usage intervals", http.StatusBadRequest)
		return
	}
	for _, interval := range body.Intervals {
		if interval.Start < now.Add(-state.UIUsageWindow).UnixMilli() || interval.End > now.UnixMilli()+5_000 || interval.End <= interval.Start {
			http.Error(w, "invalid usage interval", http.StatusBadRequest)
			return
		}
	}
	credited, err := s.stateDB.RecordUIUsage(r.Context(), body.Intervals, now)
	if err != nil {
		serverError(w, "recording UI usage", err)
		return
	}
	telemetry.RecordUIUsage(r.Context(), credited)
	writeJSON(w, struct {
		Now int64 `json:"now"`
	}{now.UnixMilli()})
}

func (s *Server) handleUIUsageDays(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	days := 30
	if value := r.URL.Query().Get("days"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 3650 {
			http.Error(w, "days must be between 0 and 3650", http.StatusBadRequest)
			return
		}
		days = parsed
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, 1-days)
	if days == 0 {
		since = time.UnixMilli(0)
	}
	stored, err := s.stateDB.UIUsageDays(r.Context(), since)
	if err != nil {
		serverError(w, "reading UI usage", err)
		return
	}
	if days == 0 {
		if len(stored) == 0 {
			writeJSON(w, stored)
			return
		}
		since, _ = time.Parse(time.DateOnly, stored[0].Date)
	}
	values := make(map[string]float64, len(stored))
	for _, day := range stored {
		values[day.Date] = day.ActiveSeconds
	}
	result := []state.UIUsageDay{}
	for date := since; !date.After(today); date = date.AddDate(0, 0, 1) {
		key := date.Format(time.DateOnly)
		result = append(result, state.UIUsageDay{Date: key, ActiveSeconds: values[key]})
	}
	writeJSON(w, result)
}
