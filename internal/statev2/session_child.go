package statev2

import "encoding/json"

// SourceChild separates native conversation hierarchy from user-created session
// links. Older metadata had only native hierarchy and remains readable.
func SourceChild(s Session) bool {
	var extra struct{ SourceChild *bool }
	if json.Unmarshal(s.Extensions, &extra) == nil && extra.SourceChild != nil {
		return *extra.SourceChild
	}
	return s.ParentID != ""
}
