package plugin

import (
	"context"
	"encoding/json"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

type PageDescriptor = plugins.PageDescriptor
type PageRequest = plugins.PageRequest
type PageDocument = plugins.PageDocument
type PageField = plugins.PageField
type PageOption = plugins.PageOption
type PageIssue = plugins.PageIssue
type PageColumn = plugins.PageColumn
type PageRow = plugins.PageRow
type PageCollection = plugins.PageCollection
type PageProperty = plugins.PageProperty
type PageDetail = plugins.PageDetail

var PageCapability = plugins.PageCapability
var PageContributionCapability = plugins.PageContributionCapability
var PluginPageCapability = plugins.PluginPageCapability
var PageModelCapability = plugins.PageModelCapability

var ValidatePageInput = plugins.ValidatePageInput

const PageProjectGrant = plugins.PageProjectGrant
const PagePluginGrant = plugins.PagePluginGrant

func PageHandler(d Description, handle func(context.Context, string, PageRequest) (PageDocument, error)) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability != "page" || call.Version.Major != 1 || call.Version.Minor > d.PageVersion().Minor || (call.Method != "read" && call.Method != "save" && call.Method != "validate") {
			return nil, Failure(ErrorNotFound)
		}
		req, err := plugins.DecodePageRequest(call.Params)
		if err != nil || !d.PageAllowed(req.PageID, d.RequestedGrants) || !d.PageRequestAllowed(req) || plugins.ValidatePageCall(call.Method, req, call.Version) != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		if call.Method == "validate" {
			allowed := false
			for _, page := range d.Pages {
				allowed = allowed || page.ID == req.PageID && page.Validate
			}
			if !allowed {
				return nil, Failure(ErrorInvalidArgument)
			}
		}
		result, err := handle(ctx, call.Method, req)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err == nil {
			var decoded PageDocument
			decoded, err = plugins.DecodePageDocument(raw)
			if err == nil {
				err = plugins.ValidatePageVersion(decoded, call.Version)
			}
		}
		return raw, err
	}
}
