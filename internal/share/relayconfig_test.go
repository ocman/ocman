package share

import "testing"

func TestResolveRelayURL(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		env        string
		builtin    string
		wantURL    string
		wantSource string
		wantErr    bool
	}{
		{
			name:       "nothing set leaves sharing local only",
			wantURL:    "",
			wantSource: "",
		},
		{
			name:       "flag wins over env and builtin",
			flag:       "https://flag.example.com",
			env:        "https://env.example.com",
			builtin:    "https://builtin.example.com",
			wantURL:    "https://flag.example.com",
			wantSource: RelaySourceFlag,
		},
		{
			name:       "env wins over builtin",
			env:        "https://env.example.com",
			builtin:    "https://builtin.example.com",
			wantURL:    "https://env.example.com",
			wantSource: RelaySourceEnv,
		},
		{
			name:       "builtin is the fallback",
			builtin:    "https://builtin.example.com",
			wantURL:    "https://builtin.example.com",
			wantSource: RelaySourceBuiltin,
		},
		{
			name:       "surrounding whitespace is trimmed",
			flag:       "  https://flag.example.com  ",
			wantURL:    "https://flag.example.com",
			wantSource: RelaySourceFlag,
		},
		{
			name:       "trailing slash is normalised away",
			flag:       "https://flag.example.com/",
			wantURL:    "https://flag.example.com",
			wantSource: RelaySourceFlag,
		},
		{
			name:       "a dev relay on loopback is accepted",
			flag:       "http://localhost:8231",
			wantURL:    "http://localhost:8231",
			wantSource: RelaySourceFlag,
		},
		// A misconfigured relay must fail loudly at startup rather than
		// silently disabling cross-machine sharing, which would look
		// like a broken feature rather than a bad flag.
		{name: "no scheme is rejected", flag: "relay.example.com", wantErr: true},
		{name: "wrong scheme is rejected", flag: "ftp://relay.example.com", wantErr: true},
		{name: "no host is rejected", flag: "https://", wantErr: true},
		{name: "unparseable is rejected", flag: "ht tp://x", wantErr: true},
		{name: "a path is rejected", flag: "https://relay.example.com/sub", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, source, err := ResolveRelayURL(tc.flag, tc.env, tc.builtin)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveRelayURL(%q, %q, %q) succeeded, want error", tc.flag, tc.env, tc.builtin)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRelayURL: %v", err)
			}
			if url != tc.wantURL {
				t.Fatalf("url = %q, want %q", url, tc.wantURL)
			}
			if source != tc.wantSource {
				t.Fatalf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

// TestResolveRelayURL_EmptyValuesFallThrough proves an empty flag or env
// var does not shadow the next source; otherwise `-relay-url=` would
// disable a baked-in default in a way nobody expects.
func TestResolveRelayURL_EmptyValuesFallThrough(t *testing.T) {
	url, source, err := ResolveRelayURL("", "   ", "https://builtin.example.com")
	if err != nil {
		t.Fatalf("ResolveRelayURL: %v", err)
	}
	if url != "https://builtin.example.com" || source != RelaySourceBuiltin {
		t.Fatalf("got (%q, %q), want the builtin default", url, source)
	}
}
