package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/NoUseFreak/ocman/internal/plugins"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Catalogs contain many individually bounded descriptions/configurations.
// ponytail: bounded unary catalogs; paginate if installations outgrow 16 MiB.
const MaxPluginResponseBytes = 16 << 20

const maxPluginRequestBytes = plugins.MaxMessageBytes + (64 << 10)

// PluginInput carries write-only secrets to the owning machine. It is never
// persisted or returned by the hub.
type PluginInput struct {
	Approval string                     `json:"approval,omitempty"`
	Grants   *[]string                  `json:"grants,omitempty"`
	Values   map[string]json.RawMessage `json:"values,omitempty"`
	Secrets  map[string]string          `json:"secrets,omitempty"`
	// DeliveryID names one conversation reply in the owner's delivery
	// backlog, for an explicit retry or discard of a dead letter.
	DeliveryID int64 `json:"deliveryId,omitempty"`
}

// PluginRequest is a closed set of host operations, not a plugin protocol tunnel.
type PluginRequest struct {
	Operation string                `json:"operation"`
	PluginID  string                `json:"pluginId,omitempty"`
	Read      bool                  `json:"read,omitempty"`
	Input     PluginInput           `json:"input,omitempty"`
	Action    plugins.ActionRequest `json:"action,omitempty"`
	Handle    string                `json:"handle,omitempty"`
}

type PluginResponse struct {
	Value json.RawMessage    `json:"value,omitempty"`
	Error *plugins.WireError `json:"error,omitempty"`
}

type PluginHandler func(context.Context, PluginRequest) PluginResponse

func (s *Server) UsePlugins(handler PluginHandler) *Server {
	s.plugins = handler
	return s
}

func (s *Server) PluginOperation(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	if s.plugins == nil {
		return nil, status.Error(codes.Unavailable, "plugins unavailable")
	}
	var operation PluginRequest
	d := json.NewDecoder(bytes.NewReader(req.Payload))
	d.DisallowUnknownFields()
	if len(req.Payload) > maxPluginRequestBytes || d.Decode(&operation) != nil || d.Decode(new(any)) != io.EOF {
		return nil, status.Error(codes.InvalidArgument, "invalid plugin operation")
	}
	if (operation.Operation == "invoke" || operation.Operation == "actions") &&
		(operation.Action.Context.OwnerID != s.instanceID || (operation.Action.OwnerID != "" && operation.Action.OwnerID != s.instanceID)) {
		return jsonResp(PluginResponse{Error: &plugins.WireError{Category: plugins.ErrorPermissionDenied}}, nil)
	}
	return jsonResp(s.plugins(ctx, operation), nil)
}

func (c *RemoteConn) PluginOperation(ctx context.Context, req PluginRequest) (PluginResponse, error) {
	client := c.Client()
	if client == nil {
		return PluginResponse{}, ErrRemoteOffline
	}
	payload, err := marshalJSON(req)
	if err != nil {
		return PluginResponse{}, err
	}
	resp, err := client.PluginOperation(ctx, &pb.JsonReq{Payload: payload}, grpc.MaxCallRecvMsgSize(MaxPluginResponseBytes+(64<<10)))
	if err != nil {
		return PluginResponse{}, err
	}
	var result PluginResponse
	err = unmarshalJSON(resp.Payload, &result)
	return result, err
}

func (m *Manager) PluginOperation(ctx context.Context, owner string, req PluginRequest) (PluginResponse, error) {
	m.mu.RLock()
	var conn *RemoteConn
	for _, mr := range m.remotes {
		if mr.conn != nil && mr.platform != nil && mr.conn.RemoteID() == owner {
			conn = mr.conn
			break
		}
	}
	m.mu.RUnlock()
	if conn == nil {
		return PluginResponse{}, ErrRemoteOffline
	}
	return conn.PluginOperation(ctx, req)
}
