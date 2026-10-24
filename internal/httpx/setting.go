package httpx

import (
	"context"
	"net/http"
)

// SettingRoute serves one stored setting: GET answers Get, POST decodes a T,
// validates and stores it, then echoes it (or answers Get when Reread).
type SettingRoute[T any] struct {
	// Name completes the "reading …" and "saving …" error log messages.
	Name string
	// Available, when set and false, answers 503 "state database not available".
	Available func() bool
	Get       func(ctx context.Context) (any, error)
	// Validate may normalize v; its error message is the 400 body.
	Validate func(v *T) error
	Set      func(ctx context.Context, v T) error
	// MaxBody bounds the POST body; zero means MaxRequestBody.
	MaxBody int64
	// Reread answers a successful POST with Get instead of the stored value.
	Reread bool
}

// Mount registers "GET path" and "POST path" through handle, typically a
// module Router's Setting or a guarded mux registration.
func (s SettingRoute[T]) Mount(handle func(pattern string, h http.HandlerFunc), path string) {
	handle("GET "+path, s.get)
	handle("POST "+path, s.post)
}

func (s SettingRoute[T]) available(w http.ResponseWriter) bool {
	if s.Available != nil && !s.Available() {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s SettingRoute[T]) get(w http.ResponseWriter, r *http.Request) {
	if s.available(w) {
		s.answer(w, r)
	}
}

func (s SettingRoute[T]) answer(w http.ResponseWriter, r *http.Request) {
	v, err := s.Get(r.Context())
	if err != nil {
		ServerError(w, "reading "+s.Name, err)
		return
	}
	WriteJSON(w, v)
}

func (s SettingRoute[T]) post(w http.ResponseWriter, r *http.Request) {
	if !s.available(w) {
		return
	}
	limit := s.MaxBody
	if limit == 0 {
		limit = MaxRequestBody
	}
	var v T
	if !ReadJSON(w, r, limit, &v) {
		return
	}
	if s.Validate != nil {
		if err := s.Validate(&v); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.Set(r.Context(), v); err != nil {
		ServerError(w, "saving "+s.Name, err)
		return
	}
	if s.Reread {
		s.answer(w, r)
		return
	}
	WriteJSON(w, v)
}
