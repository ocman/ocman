package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const webhookBodyDir = "webhook-bodies"

// webhookBodies opens <dataDir>/webhook-bodies, creating it 0700.
func (d *DB) webhookBodies() (*os.Root, string, error) {
	if d.dataDir == "" {
		return nil, "", errors.New("webhook bodies need a data directory")
	}
	dir, err := filepath.Abs(filepath.Join(d.dataDir, webhookBodyDir))
	if err != nil {
		return nil, "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("webhook body store %s is not a directory", dir)
	}
	root, err := os.OpenRoot(dir)
	return root, dir, err
}

// WriteWebhookBody stores a delivery's raw body as a file a routine session can
// read, and returns its absolute path. The name is a hash, so relay-supplied
// IDs never shape the path.
func (d *DB) WriteWebhookBody(inboxID, deliveryID string, body []byte) (string, error) {
	root, dir, err := d.webhookBodies()
	if err != nil {
		return "", err
	}
	defer root.Close()
	sum := sha256.Sum256([]byte(inboxID + ":" + deliveryID))
	name := hex.EncodeToString(sum[:]) + ".body"
	tmp := ".tmp-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, err = f.Write(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// cleanupWebhookBodies removes body files last written before cutoff, the same
// retention as the delivery rows they belong to.
func (d *DB) cleanupWebhookBodies(before int64) error {
	if d.dataDir == "" {
		return nil
	}
	dir := filepath.Join(d.dataDir, webhookBodyDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.UnixMilli(before)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
