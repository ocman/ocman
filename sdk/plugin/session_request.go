package plugin

import "encoding/json"

// sessionRequestVersion uses the exact negotiated wire version, regardless of
// a constructor's minimum version. Extension fields cannot be downgraded onto
// session 1.0. Reject those locally without tearing down other valid requests.
func sessionRequestVersion(call Call, stream *Stream) (Call, bool) {
	if call.Capability != SessionCapability.Name || call.Method != SessionRunMethod || call.Version.Major != 1 || call.Version.Minor > 1 {
		return call, false
	}
	n, ready := stream.Negotiation()
	if !ready {
		return call, false
	}
	for _, capability := range n.Capabilities {
		if capability.Name != SessionCapability.Name || capability.Version.Major != 1 {
			continue
		}
		var params SessionRunParams
		if json.Unmarshal(call.Params, &params) != nil || params.Validate() != nil {
			return call, false
		}
		if capability.Version.Minor < 1 && (params.ProjectID != "" || params.NoTools) {
			return call, true
		}
		call.Version = capability.Version
		break
	}
	return call, false
}
