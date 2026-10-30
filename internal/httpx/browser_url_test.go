package httpx

import (
	"strings"
	"testing"
)

func TestBrowserResourceURL(t *testing.T) {
	for _, raw := range []string{"https://images.example/report.png?q=1", "http://127.0.0.1:8228/x", "http://[::1]:8228/x", "https://example.com./x"} {
		if _, ok := BrowserResourceURL(raw); !ok {
			t.Errorf("valid URL refused: %s", raw)
		}
	}
	for _, raw := range []string{"", "/relative.png", "javascript:alert(1)", "data:image/png;base64,AA", "file:///etc/passwd", "https://user:secret@example.com/x", "https://example.com:0/x", "https://example.com:65536/x", "https://example.com:/x", "https://example.com;img-src*/x", "https://example.com%0d%0a/x", "https://[fe80::1%25en0]/x", "https:///no-host", "https://example.com\n/x"} {
		if _, ok := BrowserResourceURL(raw); ok {
			t.Errorf("unsafe URL accepted: %q", raw)
		}
	}
}

func TestInertPolicyDeniesExternalResources(t *testing.T) {
	for _, directive := range []string{"sandbox;", "default-src 'none';", "img-src data: blob:;", "form-action 'none';", "base-uri 'none';"} {
		if !strings.Contains(InertFileCSP, directive) {
			t.Errorf("inert file policy is missing %q", directive)
		}
	}
}
