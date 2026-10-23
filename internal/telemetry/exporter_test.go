package telemetry

import "testing"

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    target
		wantErr bool
	}{
		{
			name:  "bare host:4317 -> OTLP/HTTP on 4318",
			input: "localhost:4317",
			want:  target{endpoint: "localhost:4318", insecure: true, fromGRPC: true},
		},
		{
			name:  "bare host without port -> OTLP/HTTP insecure",
			input: "collector",
			want:  target{endpoint: "collector", insecure: true, fromGRPC: true},
		},
		{
			name:  "http -> OTLP/HTTP insecure",
			input: "http://localhost:4318",
			want:  target{endpoint: "localhost:4318", insecure: true},
		},
		{
			name:  "https -> OTLP/HTTP TLS",
			input: "https://collector.example.com:4318",
			want:  target{endpoint: "collector.example.com:4318", insecure: false},
		},
		{
			name:  "grpc:// -> OTLP/HTTP insecure",
			input: "grpc://localhost:4317",
			want:  target{endpoint: "localhost:4318", insecure: true, fromGRPC: true},
		},
		{
			name:  "grpcs:// -> OTLP/HTTP TLS, custom port kept",
			input: "grpcs://collector.example.com:443",
			want:  target{endpoint: "collector.example.com:443", insecure: false, fromGRPC: true},
		},
		{
			name:  "http with path",
			input: "http://collector.example.com/v1/otlp",
			want:  target{endpoint: "collector.example.com", urlPath: "/v1/otlp", insecure: true},
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "unknown scheme",
			input:   "tcp://localhost:1234",
			wantErr: true,
		},
		{
			name:    "malformed URL",
			input:   "http://[::1",
			wantErr: true,
		},
		{
			name:    "scheme without host",
			input:   "http://",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEndpoint(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestTargetString(t *testing.T) {
	if got := (target{endpoint: "h:4318", insecure: true}).String(); got != "http://h:4318" {
		t.Fatalf("insecure = %q", got)
	}
	if got := (target{endpoint: "h", urlPath: "/otlp"}).String(); got != "https://h/otlp" {
		t.Fatalf("tls = %q", got)
	}
}
