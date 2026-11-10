package remote

import "fmt"

func validateConnectionSources(entry ConnectionPlatformMetadata) error {
	if len(entry.SourceKeys) == 0 {
		return nil
	} // Singular older-peer metadata.
	seen := map[string]bool{}
	for _, source := range entry.SourceKeys {
		if source == "" || seen[source] {
			return fmt.Errorf("invalid remote connection source set")
		}
		seen[source] = true
	}
	if entry.SourceKey == "" || !seen[entry.SourceKey] {
		return fmt.Errorf("remote source set omits active source")
	}
	return nil
}

func copyConnectionPlatforms(entries []ConnectionPlatformMetadata) []ConnectionPlatformMetadata {
	result := append([]ConnectionPlatformMetadata(nil), entries...)
	for i := range result {
		result[i].SourceKeys = append([]string(nil), result[i].SourceKeys...)
	}
	return result
}

// A reconnect can add retained sources but cannot transfer a known physical
// source to another account. Removed accounts do not grant source reuse.
func validateConnectionSourceContinuity(previous, next []ConnectionPlatformMetadata) error {
	owners := map[string]string{}
	for _, entry := range previous {
		sources := entry.SourceKeys
		if len(sources) == 0 && entry.SourceKey != "" {
			sources = []string{entry.SourceKey}
		}
		for _, source := range sources {
			owners[entry.Kind+"\x00"+source] = entry.ID
		}
	}
	for _, entry := range next {
		sources := entry.SourceKeys
		if len(sources) == 0 && entry.SourceKey != "" {
			sources = []string{entry.SourceKey}
		}
		for _, source := range sources {
			if owner := owners[entry.Kind+"\x00"+source]; owner != "" && owner != entry.ID {
				return fmt.Errorf("remote physical source moved to another connection")
			}
		}
	}
	return nil
}
