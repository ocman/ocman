package remote

import (
	"context"
	"errors"

	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/tasksvc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type applicationTaskQuery struct {
	Operation string         `json:"operation"`
	ID        string         `json:"id,omitempty"`
	Status    tasksvc.Status `json:"status,omitempty"`
	Kind      string         `json:"kind,omitempty"`
	Limit     int            `json:"limit,omitempty"`
}

func (s *Server) UseBackgroundTasks(reader tasksvc.Reader) *Server {
	s.backgroundTasks = reader
	return s
}

func (s *Server) ApplicationTasks(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.backgroundTasks == nil {
		return nil, status.Error(codes.Unavailable, "background tasks unavailable")
	}
	var q applicationTaskQuery
	if len(req.Payload) > 4096 || unmarshalJSON(req.Payload, &q) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid task query")
	}
	switch q.Operation {
	case "list":
		if q.Limit < 1 || q.Limit > 200 || (q.Status != "" && !tasksvc.ValidStatus(q.Status)) {
			return nil, status.Error(codes.InvalidArgument, "invalid task filter")
		}
		return jsonResp(s.backgroundTasks.List(ctx, q.Status, q.Kind, q.Limit))
	case "get":
		task, err := s.backgroundTasks.Get(ctx, q.ID)
		if errors.Is(err, tasksvc.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "background task not found")
		}
		return jsonResp(task, err)
	case "active-count":
		return jsonResp(s.backgroundTasks.ActiveCount(ctx))
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown task query")
	}
}

type remoteTaskReader struct{ conn *RemoteConn }

func (h *remoteHost) BackgroundTaskReader() tasksvc.Reader { return remoteTaskReader{h.conn} }

func (r remoteTaskReader) List(ctx context.Context, state tasksvc.Status, kind string, limit int) ([]tasksvc.Task, error) {
	tasks, err := hostCall[[]tasksvc.Task](ctx, r.conn, "ApplicationTasks", applicationTaskQuery{Operation: "list", Status: state, Kind: kind, Limit: limit})
	for i := range tasks {
		tasks[i].OwnerID = r.conn.RemoteID()
	}
	return tasks, err
}

func (r remoteTaskReader) Get(ctx context.Context, id string) (tasksvc.Task, error) {
	task, err := hostCall[tasksvc.Task](ctx, r.conn, "ApplicationTasks", applicationTaskQuery{Operation: "get", ID: id})
	if status.Code(err) == codes.NotFound {
		return task, tasksvc.ErrNotFound
	}
	task.OwnerID = r.conn.RemoteID()
	return task, err
}

func (r remoteTaskReader) ActiveCount(ctx context.Context) (int, error) {
	return hostCall[int](ctx, r.conn, "ApplicationTasks", applicationTaskQuery{Operation: "active-count"})
}
