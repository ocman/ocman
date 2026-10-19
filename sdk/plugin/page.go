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

var PageCapability = plugins.PageCapability

const PageProjectGrant = plugins.PageProjectGrant

func PageHandler(d Description, handle func(context.Context, string, PageRequest) (PageDocument, error)) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability != "page" || call.Version != PageCapability.Version || (call.Method != "read" && call.Method != "save") {
			return nil, Failure(ErrorNotFound)
		}
		req, err := plugins.DecodePageRequest(call.Params)
		if err != nil || !d.PageAllowed(req.PageID, d.RequestedGrants) {
			return nil, Failure(ErrorInvalidArgument)
		}
		result, err := handle(ctx, call.Method, req)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err == nil {
			_, err = plugins.DecodePageDocument(raw)
		}
		return raw, err
	}
}
