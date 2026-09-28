package state

import (
	"context"
	"encoding/base64"
	"fmt"
)

// SetSecretSetting stores value under key sealed with the same AES-GCM key
// that protects remote tokens, so a copied setting row alone reveals nothing.
func (d *DB) SetSecretSetting(ctx context.Context, key, value string) error {
	sealed, err := d.encryptToken(ctx, value)
	if err != nil {
		return err
	}
	return d.SetSetting(ctx, key, base64.RawStdEncoding.EncodeToString(sealed))
}

// GetSecretSetting opens a value written by SetSecretSetting.
func (d *DB) GetSecretSetting(ctx context.Context, key string) (string, bool, error) {
	stored, ok, err := d.GetSetting(ctx, key)
	if err != nil || !ok {
		return "", ok, err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(stored)
	if err != nil {
		return "", false, fmt.Errorf("decoding secret setting %q: %w", key, err)
	}
	value, err := d.decryptToken(ctx, sealed)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
