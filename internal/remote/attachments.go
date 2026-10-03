package remote

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/NoUseFreak/ocman/internal/composerattachments"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const attachmentChunkBytes = 32 << 10

func (h *remoteHost) SaveComposerAttachment(ctx context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // A local read failure must stop the owner and remove its partial file.
	stream, err := client.SaveComposerAttachment(ctx)
	if err != nil {
		return nil, err
	}
	metadata, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&pb.JsonReq{Payload: metadata}); err != nil {
		return nil, err
	}
	buffer := make([]byte, attachmentChunkBytes)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if err := stream.Send(&pb.JsonReq{Payload: bytes.Clone(buffer[:n])}); err != nil {
				if errors.Is(err, io.EOF) {
					if _, replyErr := stream.CloseAndRecv(); replyErr != nil {
						err = replyErr
					}
				}
				return nil, attachmentRPCError(err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return nil, attachmentRPCError(err)
	}
	var saved hostsvc.ComposerAttachment
	return &saved, unmarshalJSON(response.Payload, &saved)
}

func attachmentRPCError(err error) error {
	if status.Code(err) == codes.ResourceExhausted {
		return composerattachments.ErrTooLarge
	}
	return err
}

func (s *Server) SaveComposerAttachment(stream pb.Ocman_SaveComposerAttachmentServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if len(first.Payload) > attachmentChunkBytes {
		return status.Error(codes.InvalidArgument, "attachment metadata too large")
	}
	var req hostsvc.ComposerAttachmentRequest
	if err := unmarshalJSON(first.Payload, &req); err != nil {
		return status.Error(codes.InvalidArgument, "invalid attachment metadata")
	}
	saved, err := s.host.SaveComposerAttachment(stream.Context(), req, &attachmentReader{stream: stream})
	if errors.Is(err, composerattachments.ErrTooLarge) {
		return status.Error(codes.ResourceExhausted, err.Error())
	}
	response, err := jsonResp(saved, err)
	if err != nil {
		return err
	}
	return stream.SendAndClose(response)
}

type attachmentReader struct {
	stream  pb.Ocman_SaveComposerAttachmentServer
	pending []byte
}

func (r *attachmentReader) Read(buffer []byte) (int, error) {
	for len(r.pending) == 0 {
		chunk, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		if len(chunk.Payload) > attachmentChunkBytes {
			return 0, status.Error(codes.InvalidArgument, "attachment chunk too large")
		}
		r.pending = chunk.Payload
	}
	n := copy(buffer, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
