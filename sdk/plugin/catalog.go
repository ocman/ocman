package plugin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/ocman/ocman/sdk/protocol"
)

// Aliases and constants for the launch-time catalog delivered on fd 3.
type (
	CatalogAgent  = protocol.CatalogAgent
	CatalogModel  = protocol.CatalogModel
	CatalogModels = protocol.CatalogModels
	LaunchCatalog = protocol.LaunchCatalog
)

const (
	CatalogProjectSetting = protocol.CatalogProjectSetting
	CatalogLaunchKey      = protocol.CatalogLaunchKey
)

// Configuration is the fd-3 payload. Values holds every declared setting as
// raw JSON; Catalog is the host-injected snapshot the plugin receives when it
// opted in via WantsCatalog, its project setting resolved, and a local
// platform instance was reachable — nil otherwise. The catalog is host-owned
// launch configuration, so it is separated from Values instead of arriving as
// a setting-shaped key.
type Configuration struct {
	Values  map[string]json.RawMessage
	Catalog *LaunchCatalog
}

// ParseConfiguration decodes the single JSON line the host writes to fd 3.
// Every member is a declared setting except an object-valued catalog member.
func ParseConfiguration(line []byte) (Configuration, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(line), &values) != nil {
		return Configuration{}, errors.New("invalid configuration")
	}
	config := Configuration{Values: values}
	// Setting values are never objects, so only an object is the host's
	// catalog; anything else is an opted-out plugin's own "catalog" setting.
	if raw, ok := values[CatalogLaunchKey]; ok && bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		delete(values, CatalogLaunchKey)
		config.Catalog = new(LaunchCatalog)
		if json.Unmarshal(raw, config.Catalog) != nil {
			return Configuration{}, errors.New("invalid catalog")
		}
	}
	return config, nil
}

// ReadConfiguration reads and closes inherited fd 3, the one JSON line the
// host writes before the serve hello. Secrets arrive only here, so read it
// before emitting the hello and keep the values in process memory. Describe
// mode has no configuration descriptor.
func ReadConfiguration() (Configuration, error) {
	f := os.NewFile(3, "config")
	if f == nil {
		return Configuration{}, errors.New("missing configuration")
	}
	defer f.Close()
	line, err := bufio.NewReader(io.LimitReader(f, protocol.MaxMessageBytes+1)).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Configuration{}, err
	}
	return ParseConfiguration(line)
}
