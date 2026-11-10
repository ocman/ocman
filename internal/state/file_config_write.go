package state

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/configfile"
)

var ErrExternalSecret = errors.New("secret is externally managed; change it at its source")

// A unique encrypted slot is written before publishing its reference. If the
// file write fails, the old reference and old secret remain intact. Unreferenced
// slots are harmless; never update an active slot before the file save succeeds.
func (d *DB) storeConfigSecret(ctx context.Context, value string) (string, error) {
	aead, err := d.configCipher(ctx, true)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), nil)
	id := uuid.NewString()
	if err := d.setSetting(ctx, "config.secret/"+id, base64.RawStdEncoding.EncodeToString(sealed)); err != nil {
		return "", err
	}
	return "secret://ocman/" + id, nil
}

func (d *DB) loadConfigSecret(ctx context.Context, id string) (string, error) {
	var stored string
	if err := d.reader().QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, "config.secret/"+id).Scan(&stored); err != nil {
		return "", err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(stored)
	if err != nil {
		return "", err
	}
	aead, err := d.configCipher(ctx, false)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("configuration secret ciphertext is truncated")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
	return string(plain), err
}

func (d *DB) writeFileSetting(ctx context.Context, key, value string) error {
	return d.writeFileSettings(ctx, map[string]string{key: value})
}

func (d *DB) writeFileSettings(ctx context.Context, values map[string]string) error {
	f := d.fileConfig.Load()
	return f.file.Update(func(doc *configfile.Document) error {
		if err := validateFileSettings(doc); err != nil {
			return err
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := d.applyFileSetting(ctx, f, doc, key, values[key]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *DB) applyFileSetting(ctx context.Context, f *fileConfig, doc *configfile.Document, key, value string) error {
	old, _, err := doc.Get("settings", key)
	if err != nil {
		return err
	}
	var next any = value
	switch {
	case isFileSecret(key):
		next, err = d.replaceFileSecret(ctx, f, old, value)
	case key == "judge_endpoint":
		next, err = d.replaceEndpointSecret(ctx, f, old, value)
	default:
		// Existing setting interfaces use strings, with JSON for structured
		// values. Keep objects/arrays editable as native YAML.
		if strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") {
			var decoded any
			if json.Unmarshal([]byte(value), &decoded) == nil {
				next = decoded
			}
		}
	}
	if err != nil {
		return err
	}
	return doc.Set("settings", key, next)
}

func (d *DB) replaceFileSecret(ctx context.Context, f *fileConfig, old, value string) (string, error) {
	if old != "" {
		ref, err := configfile.ParseSecret(old)
		if err != nil {
			return "", err
		}
		if ref.Provider != "ocman" {
			return "", ErrExternalSecret
		}
		previous, err := f.resolver.Resolve(ctx, old)
		if err != nil {
			return "", err
		}
		if previous == value {
			return old, nil
		}
	}
	if value == "" {
		return "", nil
	}
	return d.storeConfigSecret(ctx, value)
}

func (d *DB) replaceEndpointSecret(ctx context.Context, f *fileConfig, old, value string) (map[string]any, error) {
	before, err := endpointFields(old)
	if err != nil {
		return nil, err
	}
	next, err := endpointFields(value)
	if err != nil {
		return nil, err
	}
	previous, _ := before["apiKey"].(string)
	secret, _ := next["apiKey"].(string)
	if previous != "" {
		ref, err := configfile.ParseSecret(previous)
		if err != nil {
			return nil, err
		}
		if ref.Provider != "ocman" && before["endpoint"] != next["endpoint"] {
			return nil, ErrExternalSecret
		}
		// Existing endpoints return resolved credentials to the backend save
		// path. Preserve the original reference when the secret is unchanged.
		resolved, err := f.resolver.Resolve(ctx, previous)
		if err != nil {
			return nil, err
		}
		if resolved == secret {
			next["apiKey"] = previous
			return next, nil
		}
	}
	ref, err := d.replaceFileSecret(ctx, f, previous, secret)
	if err != nil {
		return nil, err
	}
	next["apiKey"] = ref
	return next, nil
}
