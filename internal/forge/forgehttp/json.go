package forgehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// Fetch GETs path relative to a client's API root with its auth applied.
type Fetch func(ctx context.Context, path string) ([]byte, forge.RateLimit, int, error)

// StatusError maps a non-200 status to the error a caller returns.
type StatusError func(status int, rl forge.RateLimit) error

// UpstreamStatus reports any non-200 status as a ResponseError.
func UpstreamStatus(status int, rl forge.RateLimit) error {
	return &ResponseError{Status: status, RateLimit: rl}
}

// GetJSON GETs path and decodes a 200 response into T. Other statuses return
// statusErr's result; what names the payload in decode errors.
func GetJSON[T any](ctx context.Context, fetch Fetch, path, what string, statusErr StatusError) (T, forge.RateLimit, error) {
	var out T
	body, rl, status, err := fetch(ctx, path)
	if err != nil {
		return out, rl, err
	}
	if status != http.StatusOK {
		return out, rl, statusErr(status, rl)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, rl, fmt.Errorf("decoding %s: %w", what, err)
	}
	return out, rl, nil
}

// List GETs one page of rows and maps them with conv, dropping rows it
// rejects. A 429 yields no rows and no error so callers can surface rl.
func List[T, U any](ctx context.Context, fetch Fetch, path, what string, conv func(T) (U, bool)) ([]U, forge.RateLimit, error) {
	limited := false
	raw, rl, err := GetJSON[[]T](ctx, fetch, path, what, func(status int, rl forge.RateLimit) error {
		if status == http.StatusTooManyRequests {
			limited = true
			return nil
		}
		return UpstreamStatus(status, rl)
	})
	if err != nil || limited {
		return nil, rl, err
	}
	out := make([]U, 0, len(raw))
	for _, r := range raw {
		if u, ok := conv(r); ok {
			out = append(out, u)
		}
	}
	return out, rl, nil
}

// CurrentUser resolves the authenticated login from path. It returns
// forge.ErrUnauthenticated without a token or on a 401/403.
func CurrentUser(ctx context.Context, fetch Fetch, host, path, token string) (forge.CurrentUser, error) {
	if token == "" {
		return forge.CurrentUser{}, forge.ErrUnauthenticated
	}
	raw, _, err := GetJSON[struct {
		Login string `json:"login"`
	}](ctx, fetch, path, "/user", func(status int, rl forge.RateLimit) error {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return forge.ErrUnauthenticated
		}
		return UpstreamStatus(status, rl)
	})
	if err != nil {
		return forge.CurrentUser{}, err
	}
	return forge.CurrentUser{Host: host, Login: raw.Login}, nil
}
