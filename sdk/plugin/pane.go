package plugin

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

type PaneDescriptor = plugins.PaneDescriptor
type PaneRead = plugins.PaneRead
type PaneTree = plugins.PaneTree
type TreeNode = plugins.TreeNode

var PaneCapability = plugins.PaneCapability

const PaneProjectGrant = plugins.PaneProjectGrant

// PaneHandler serves a unary, cancellable read. The host checks grants and
// owner routing before passing a directory to this handler.
func PaneHandler(description Description, read func(context.Context, PaneRead) (PaneTree, error)) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability != PaneCapability.Name || call.Version != PaneCapability.Version || call.Method != "read" {
			return nil, Failure(ErrorNotFound)
		}
		request, err := plugins.DecodePaneRead(call.Params)
		if err != nil || !description.PaneAllowed(request.PaneID, description.RequestedGrants) {
			return nil, Failure(ErrorInvalidArgument)
		}
		tree, err := read(ctx, request)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(tree)
		if err == nil {
			_, err = plugins.DecodePaneTree(data)
		}
		return data, err
	}
}
