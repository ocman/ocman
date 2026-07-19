package ocapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthHTTP(t *testing.T) {
	const password = "test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != DefaultUsername || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tests := []struct {
		name    string
		auth    Auth
		wantErr bool
	}{
		{"valid credentials", New(password), false},
		{"authentication disabled", New(""), true},
		{"invalid credentials", New("wrong"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: tt.auth.Transport(http.DefaultTransport)}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			resp, err := client.Do(req)
			if tt.wantErr {
				if !errors.Is(err, ErrAuthentication) {
					t.Fatalf("error = %v, want ErrAuthentication", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			resp.Body.Close()
		})
	}
}

func TestAuthDisabledPreservesUnauthenticatedServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Transport: New("").Transport(http.DefaultTransport)}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
}

func TestAuthEnabledPreservesUnauthenticatedServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Transport: New("managed-secret").Transport(http.DefaultTransport)}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
}

func TestAuthRedactsSecret(t *testing.T) {
	const secret = "never-print-this"
	auth := New(secret)
	b, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprint(auth), fmt.Sprintf("%+v", auth), fmt.Sprintf("%#v", auth), string(b)} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("secret exposed by formatting: %q", rendered)
		}
	}
}
