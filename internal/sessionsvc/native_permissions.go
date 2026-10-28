package sessionsvc

import "github.com/ocman/ocman/internal/platforms"

func validateNativePermissionUpdate(req platforms.SetPermissionRulesRequest) error {
	if req.NativeMode == nil {
		return nil
	}
	if *req.NativeMode != "default" && *req.NativeMode != "auto" {
		return validation("native permission mode must be default or auto")
	}
	if req.Rules != nil {
		return validation("change native permission mode and ocman rules separately")
	}
	return nil
}
