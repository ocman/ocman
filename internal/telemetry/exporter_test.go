package telemetry

import "testing"

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     target
		wantErr  bool
	}{
		{
			name:  "bare host:port -> gRPC insecure",
			input: "localhost:4317",
			want:  target{protocol: protoGRPC, endpoint: "localhost:4317", insecure: true},
		},
		{
			name:  "http -> OTLP/HTTP insecure",
			input: "http://localhost:4318",
			want:  target{protocol: protoHTTP, endpoint: "localhost:4318", insecure: true},
		},
		{
			name:  "https -> OTLP/HTTP TLS",
			input: "https://collector.example.com:4318",
			want:  target{protocol: protoHTTP, endpoint: "collector.example.com:4318", insecure: false},
		},
		{
			name:  "grpc:// -> OTLP/gRPC insecure",
			input: "grpc://localhost:4317",
			want:  target{protocol: protoGRPC, endpoint: "localhost:4317", insecure: true},
		},
		{
			name:  "grpcs:// -> OTLP/gRPC TLS",
			input: "grpcs://collector.example.com:4317",
			want:  target{protocol: protoGRPC, endpoint: "collector.example.com:4317", insecure: false},
		},
		{
			name:  "http with path",
			input: "http://collector.example.com/v1/otlp",
			want:  target{protocol: protoHTTP, endpoint: "collector.example.com", urlPath: "/v1/otlp", insecure: true},
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
