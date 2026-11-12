package projectforge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestForgeFailureCertificateCauses(t *testing.T) {
	for _, err := range []error{
		&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		x509.UnknownAuthorityError{},
		x509.HostnameError{},
		x509.CertificateInvalidError{},
	} {
		if got := forgeFailureCause(fmt.Errorf("secret: %w", err)); got != "tls" {
			t.Errorf("%T classified as %q", err, got)
		}
	}
	if got := forgeFailureCause(io.ErrUnexpectedEOF); got != "eof" {
		t.Errorf("unexpected EOF classified as %q", got)
	}
}

func TestRequestContextState(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := requestContextState(ctx); got != "canceled" {
		t.Errorf("state=%q", got)
	}
	ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if got := requestContextState(ctx); got != "deadline" {
		t.Errorf("state=%q", got)
	}
}
