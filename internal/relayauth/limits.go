package relayauth

import (
	"net"
	"net/http"
	"strings"
	"time"
)

type requestWindow struct {
	start time.Time
	count int
}

// Limits are per process, matching the relay's single-writer disk store.
// Forwarded addresses are trusted only behind a proxy that overwrites them.
func (s *Service) limited(action string, maxRequests int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if s.cfg.TrustProxy {
			forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
			if net.ParseIP(forwarded) != nil {
				ip = forwarded
			}
		}
		key := action + ":" + ip
		now := s.cfg.Now()
		s.mu.Lock()
		if len(s.limits) >= 10000 {
			for key, window := range s.limits {
				if now.Sub(window.start) >= time.Minute {
					delete(s.limits, key)
				}
			}
		}
		window, exists := s.limits[key]
		if !exists || now.Sub(window.start) >= time.Minute {
			window = requestWindow{start: now}
		}
		allowed := window.count < maxRequests && (exists || len(s.limits) < 10000)
		if allowed {
			window.count++
			s.limits[key] = window
		}
		s.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		if !allowed {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many sign-in requests; try again later", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
