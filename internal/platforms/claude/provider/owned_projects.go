package claudeprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// OwnedProjectRoots is an authoritative historical snapshot. An absent ID was
// not launched by ocman; a present empty root is a legacy unknown, not absence.
func OwnedProjectRoots(statePath string) (map[string]string, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	var all map[string]owned
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	if all == nil {
		return nil, fmt.Errorf("invalid session ownership snapshot")
	}
	roots := make(map[string]string, len(all))
	for id, session := range all {
		if session.ProjectRoot != "" && !filepath.IsAbs(session.ProjectRoot) {
			return nil, fmt.Errorf("invalid historical project root for session %s", id)
		}
		roots[id] = session.ProjectRoot
	}
	return roots, nil
}
