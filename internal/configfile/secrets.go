// Package configfile owns ocman's declarative configuration and secret references.
package configfile

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const secretPrefix = "secret://"

// SecretRef is an opaque provider reference, never a resolved credential.
type SecretRef struct {
	Provider  string
	Reference string
}

var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseSecret splits before decoding so escaped slashes belong to the reference.
// Query and fragment characters must be escaped; references are not templates.
func ParseSecret(value string) (SecretRef, error) {
	if !strings.HasPrefix(value, secretPrefix) {
		return SecretRef{}, errors.New("secret must use a secret://provider/reference")
	}
	provider, reference, ok := strings.Cut(strings.TrimPrefix(value, secretPrefix), "/")
	if !ok || !providerName.MatchString(provider) || reference == "" || strings.ContainsAny(reference, "?#\r\n") {
		return SecretRef{}, errors.New("invalid secret reference")
	}
	reference, err := url.PathUnescape(reference)
	if err != nil || strings.ContainsAny(reference, "\x00\r\n") {
		return SecretRef{}, errors.New("invalid secret reference encoding")
	}
	if provider == "file" {
		reference = "/" + reference
	}
	if provider == "env" && !envName.MatchString(reference) {
		return SecretRef{}, errors.New("invalid secret environment variable name")
	}
	return SecretRef{Provider: provider, Reference: reference}, nil
}

// SecretLoader retrieves a credential. Implementations must not expose it in errors.
type SecretLoader func(context.Context, string) (string, error)

// Resolver uses an explicit registry, not shell expansion or recursive evaluation.
type Resolver struct {
	Loaders map[string]SecretLoader
}

func NewResolver(ocman SecretLoader) Resolver {
	return Resolver{Loaders: map[string]SecretLoader{
		"env": func(_ context.Context, name string) (string, error) {
			value, ok := os.LookupEnv(name)
			if !ok || value == "" {
				return "", errors.New("secret environment variable is missing or empty")
			}
			return value, nil
		},
		"file":  loadSecretFile,
		"ocman": ocman,
	}}
}

func (r Resolver) Resolve(ctx context.Context, value string) (string, error) {
	ref, err := ParseSecret(value)
	if err != nil {
		return "", err
	}
	loader := r.Loaders[ref.Provider]
	if loader == nil {
		return "", errors.New("unsupported secret provider")
	}
	secret, err := loader(ctx, ref.Reference)
	if err != nil {
		return "", errors.New("secret provider could not resolve credential")
	}
	if secret == "" {
		return "", errors.New("resolved secret is empty")
	}
	return secret, nil
}

func loadSecretFile(_ context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("secret file path must be absolute")
	}
	// Nonblocking open lets us reject FIFOs without waiting for a writer.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("cannot open secret file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("secret file must be a regular file")
	}
	const limit = 1 << 20
	value, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(value) > limit {
		return "", errors.New("cannot read secret file within size limit")
	}
	return strings.TrimRight(string(value), "\r\n"), nil
}
