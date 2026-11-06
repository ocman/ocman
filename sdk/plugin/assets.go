package plugin

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"

	"github.com/ocman/ocman/sdk/protocol"
)

type IconDescriptor = protocol.IconDescriptor
type AssetRequest = protocol.AssetRequest
type AssetResult = protocol.AssetResult

const MaxIconBytes = protocol.MaxIconBytes

// AssetHandler serves the exact declared icon from assets, typically embed.FS.
// Dispatch calls with Capability == "asset" here. No capability declaration or
// grant is required. The file is read with a hard limit, never from an inferred
// process working directory. Other assets are not exposed.
func AssetHandler(description Description, assets fs.FS) Handler {
	return func(ctx context.Context, c Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if c.Capability != "asset" || c.Version != protocol.AssetCapability.Version || c.Method != "read" {
			return nil, Failure(ErrorNotFound)
		}
		req, err := protocol.DecodeAssetRequest(c.Params)
		if err != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		if description.Icon == nil || req.Path != description.Icon.Path || assets == nil {
			return nil, Failure(ErrorNotFound)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := assets.Open(req.Path)
		if err != nil {
			return nil, Failure(ErrorNotFound)
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, MaxIconBytes+1))
		if err != nil {
			return nil, Failure(ErrorInternal)
		}
		result, err := protocol.EncodeAssetResult(data)
		if err != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		return result, nil
	}
}
