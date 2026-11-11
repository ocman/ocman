package usagewire

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// ReadJSON rejects extra fields, trailing JSON and oversized bodies. Neither
// invalid bodies nor decoder errors are reflected into logs or responses.
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dst)
	if err == nil {
		err = decoder.Decode(&struct{}{})
		if errors.Is(err, io.EOF) {
			return true
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
	} else {
		http.Error(w, "invalid usage payload", http.StatusBadRequest)
	}
	return false
}
