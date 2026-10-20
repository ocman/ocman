package remote

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

const creationErrorDomain = "ocman.session-creation"

func withCreationRejected(err error) error {
	s, detailErr := status.Convert(err).WithDetails(&errdetails.ErrorInfo{Domain: creationErrorDomain, Reason: "REJECTED_BEFORE_ALLOCATION"})
	if detailErr != nil {
		return err
	} // losing certainty must fail safe
	return s.Err()
}

func creationWasRejected(err error) bool {
	if err == nil {
		return false
	}
	for _, detail := range status.Convert(err).Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == creationErrorDomain && info.Reason == "REJECTED_BEFORE_ALLOCATION" {
			return true
		}
	}
	return false
}
