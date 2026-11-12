package projectforge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/ocman/ocman/internal/forge/forgehttp"
)

// Only fixed labels leave this classifier. Error strings, URL fields, DNS
// names, certificate contents and upstream bodies are never safe diagnostics.
func forgeFailureCause(err error) string {
	var response *forgehttp.ResponseError
	var network net.Error
	var dns *net.DNSError
	var record tls.RecordHeaderError
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &response):
		return "http_status"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.As(err, &network) && network.Timeout():
		return "timeout"
	case errors.As(err, &dns):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.As(err, &record), errors.As(err, &verification), errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid):
		return "tls"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	default:
		return "other"
	}
}

func requestContextState(ctx context.Context) string {
	switch ctx.Err() {
	case context.Canceled:
		return "canceled"
	case context.DeadlineExceeded:
		return "deadline"
	default:
		return "active"
	}
}
