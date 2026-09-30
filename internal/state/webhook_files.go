package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const webhookFileDir = "webhook-deliveries"

// webhookFiles opens <dataDir>/webhook-deliveries, creating it 0700.
func (d *DB) webhookFiles() (*os.Root, string, error) {
	if d.dataDir == "" {
		return nil, "", errors.New("webhook files need a data directory")
	}
	dir, err := filepath.Abs(filepath.Join(d.dataDir, webhookFileDir))
	if err != nil {
		return nil, "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("webhook file store %s is not a directory", dir)
	}
	root, err := os.OpenRoot(dir)
	return root, dir, err
}

// Webhook delivery file kinds.
const (
	WebhookBodyFile    = "body"
	WebhookHeadersFile = "headers"
)

// WriteWebhookFile stores one part of a delivery (WebhookBodyFile or
// WebhookHeadersFile) as a file a routine session can read, and returns its
// absolute path. The name is a hash, so relay-supplied IDs never shape the
// path; both parts of a delivery share it and differ only in extension.
func (d *DB) WriteWebhookFile(inboxID, deliveryID, kind string, data []byte) (string, error) {
	if kind != WebhookBodyFile && kind != WebhookHeadersFile {
		return "", fmt.Errorf("unknown webhook file kind %q", kind)
	}
	root, dir, err := d.webhookFiles()
	if err != nil {
		return "", err
	}
	defer root.Close()
	sum := sha256.Sum256([]byte(inboxID + ":" + deliveryID))
	name := hex.EncodeToString(sum[:]) + "." + kind
	tmp := ".tmp-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
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

// webhookFilesGCBatch bounds how many directory entries one read allocates.
const webhookFilesGCBatch = 256

// cleanupWebhookFiles removes delivery files last written before cutoff, the
// same retention as the delivery rows they belong to.
func (d *DB) cleanupWebhookFiles(before int64) error {
	if d.dataDir == "" {
		return nil
	}
	dir := filepath.Join(d.dataDir, webhookFileDir)
	f, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	cutoff := time.UnixMilli(before)
	for {
		entries, err := f.ReadDir(webhookFilesGCBatch)
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if errors.Is(err, io.EOF) || len(entries) == 0 {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
