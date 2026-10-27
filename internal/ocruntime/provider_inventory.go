package ocruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Instances lists managed supervisor handles for this exact root. Discovery
// alone proves nothing about liveness, configuration or session ownership.
func (r *BackendRuntime) Instances(root string) ([]*Instance, error) {
	entries, err := os.ReadDir(r.Directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(root))
	prefix := r.PlatformID + "-" + hex.EncodeToString(hash[:6]) + "-"
	var instances []*Instance
	for _, entry := range entries {
		if !entry.IsDir() || !providerName.MatchString(entry.Name()) || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		instances = append(instances, &Instance{Endpoint: "unix://" + filepath.Join(r.Directory, entry.Name(), "s"),
			ID: entry.Name(), Kind: KindProviderTmux, RepoRoot: root})
	}
	return instances, nil
}
