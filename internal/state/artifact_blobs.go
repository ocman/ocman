package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// MaxArtifactFileBytes caps one artifact file payload.
const MaxArtifactFileBytes = 50 << 20

var (
	// ErrArtifactTooLarge rejects a payload above MaxArtifactFileBytes.
	ErrArtifactTooLarge = errors.New("artifact file exceeds 50 MB")
	artifactSHA         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// artifactBlobs opens <dataDir>/artifacts/blobs, creating it 0700.
func (d *DB) artifactBlobs() (*os.Root, error) {
	if d.dataDir == "" {
		return nil, errors.New("artifact store needs a data directory")
	}
	root, err := os.OpenRoot(d.dataDir)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"artifacts", "blobs"} {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			_ = root.Close()
			return nil, err
		}
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			_ = root.Close()
			return nil, fmt.Errorf("artifact store %s is not a directory", name)
		}
		next, err := root.OpenRoot(name)
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

// PutArtifactBlob stores r content-addressed by SHA-256 and returns its hash
// and size. Identical content is stored once.
func (d *DB) PutArtifactBlob(r io.Reader) (string, int64, error) {
	d.artifactMu.Lock()
	defer d.artifactMu.Unlock()
	root, err := d.artifactBlobs()
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	tmp := ".tmp-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = root.Remove(tmp) }()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, MaxArtifactFileBytes+1))
	if err == nil && size > MaxArtifactFileBytes {
		err = ErrArtifactTooLarge
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if _, err := root.Lstat(sum); err == nil {
		return sum, size, nil
	}
	if err := root.Rename(tmp, sum); err != nil {
		return "", 0, err
	}
	dir, err := root.Open(".")
	if err != nil {
		return "", 0, err
	}
	err = dir.Sync()
	_ = dir.Close()
	return sum, size, err
}

// OpenArtifactBlob opens a stored payload for reading.
func (d *DB) OpenArtifactBlob(sum string) (*os.File, error) {
	if !artifactSHA.MatchString(sum) {
		return nil, ErrArtifactNotFound
	}
	root, err := d.artifactBlobs()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(sum)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrArtifactNotFound
	}
	return f, err
}

// artifactBlobSize reports a stored blob's size; caller holds artifactMu.
func (d *DB) artifactBlobSize(sum string) (int64, error) {
	root, err := d.artifactBlobs()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	info, err := root.Lstat(sum)
	if err != nil || !info.Mode().IsRegular() {
		return 0, ErrArtifactInvalid
	}
	return info.Size(), nil
}

// removeArtifactBlobs deletes blobs; caller holds artifactMu and has checked
// no item references them.
func (d *DB) removeArtifactBlobs(sums []string) error {
	if len(sums) == 0 {
		return nil
	}
	root, err := d.artifactBlobs()
	if err != nil {
		return err
	}
	defer root.Close()
	for _, sum := range sums {
		if err := root.Remove(sum); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
