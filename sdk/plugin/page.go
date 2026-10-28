package plugin

import (
	"context"
	"encoding/json"
	"github.com/ocman/ocman/sdk/protocol"
)

type PageDescriptor = protocol.PageDescriptor
type PageRequest = protocol.PageRequest
type PageDocument = protocol.PageDocument
type PageField = protocol.PageField
type PageOption = protocol.PageOption
type PageIssue = protocol.PageIssue
type PageColumn = protocol.PageColumn
type PageSummary = protocol.PageSummary
type PageRow = protocol.PageRow
type PageCollection = protocol.PageCollection
type PageProperty = protocol.PageProperty
type PageDetail = protocol.PageDetail

var PageCapability = protocol.PageCapability
var PageContributionCapability = protocol.PageContributionCapability
var PluginPageCapability = protocol.PluginPageCapability
var PageModelCapability = protocol.PageModelCapability
var PageTableCapability = protocol.PageTableCapability

var ValidatePageInput = protocol.ValidatePageInput

const PageProjectGrant = protocol.PageProjectGrant
const PagePluginGrant = protocol.PagePluginGrant

func PageHandler(d Description, handle func(context.Context, string, PageRequest) (PageDocument, error)) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability != "page" || call.Version.Major != 1 || call.Version.Minor > d.PageVersion().Minor || (call.Method != "read" && call.Method != "save" && call.Method != "validate") {
			return nil, Failure(ErrorNotFound)
		}
		req, err := protocol.DecodePageRequest(call.Params)
		if err != nil || !d.PageAllowed(req.PageID, d.RequestedGrants) || !d.PageRequestAllowed(req) || protocol.ValidatePageCall(call.Method, req, call.Version) != nil {
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
			decoded, err = protocol.DecodePageDocument(raw)
			if err == nil {
				err = protocol.ValidatePageVersion(decoded, call.Version)
			}
		}
		return raw, err
	}
}
