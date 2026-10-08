package forgehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func TestResponseErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status        int
		header, value string
		limited       bool
		sentinel      error
	}{
		{500, "", "", false, nil},
		{401, "", "", false, forge.ErrUnauthenticated},
		{403, "", "", false, forge.ErrUnauthenticated},
		{403, "X-RateLimit-Remaining", "0", true, forge.ErrRateLimited},
		{403, "Retry-After", "17", true, forge.ErrRateLimited},
	} {
		t.Run(http.StatusText(tc.status)+tc.header, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.header != "" {
					w.Header().Set(tc.header, tc.value)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("secret"))
			}))
			defer srv.Close()
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			body, rl, status, err := Get(context.Background(), srv.Client(), req)
			var response *ResponseError
			if !errors.As(err, &response) || status != tc.status || response.Status != status || rl.Limited != tc.limited || body != nil {
				t.Fatalf("body=%s status=%d rl=%v err=%v", body, status, rl, err)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("error=%v want %v", err, tc.sentinel)
			}
			if strings.Contains(response.Error(), "secret") {
				t.Fatal(response.Error())
			}
		})
	}
}

func TestParseRateLimitRetryDate(t *testing.T) {
	date := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	h := http.Header{"Retry-After": []string{date.Format(http.TimeFormat)}}
	if rl := ParseRateLimit(h, true); !rl.Limited || !rl.ResetAt.Equal(date) {
		t.Fatalf("rate limit=%v", rl)
	}
	h.Set("Retry-After", "invalid")
	h.Set("X-RateLimit-Reset", "123")
	if rl := ParseRateLimit(h, true); !rl.ResetAt.Equal(time.Unix(123, 0)) {
		t.Fatal(rl)
	}
	h.Set("X-RateLimit-Reset", "invalid")
	if rl := ParseRateLimit(h, false); rl.Limited || !rl.ResetAt.IsZero() {
		t.Fatal(rl)
	}
}
