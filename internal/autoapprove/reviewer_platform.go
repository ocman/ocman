package autoapprove

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
)

func (s *Service) namedReviewerConfigured(ctx context.Context) bool {
	if s.deps.ReviewerPlatform == nil || s.deps.Store == nil {
		return false
	}
	ref, _, err := s.deps.Store.GetSetting(ctx, JudgeModelSettingKey)
	prefix, _ := platforms.SplitModel(ref)
	return err != nil || strings.HasPrefix(string(prefix), "cnx_")
}

func (s *Service) reviewerAdapter() platforms.Platform {
	if s.deps.ReviewerPlatform != nil {
		return s.deps.ReviewerPlatform()
	}
	return s.OpencodeAdapter()
}

func (s *Service) reviewerPort(directory string) string {
	if s.deps.ReviewerPlatform == nil {
		return s.openCodePort(directory)
	}
	if p, ok := s.reviewerAdapter().(platforms.InstancePortProvider); ok && p.InstancePorts() != nil {
		return p.InstancePorts().DiscoverOpenCodePort(directory)
	}
	return ""
}

func (s *Service) reviewerHTTPClient() *http.Client {
	if p, ok := s.reviewerAdapter().(platforms.OpenCodeEventClientProvider); ok {
		if client := p.OpenCodeEventClient(); client != nil {
			return client
		}
	}
	return &http.Client{Transport: unavailableReviewerTransport{}}
}

type unavailableReviewerTransport struct{}

func (unavailableReviewerTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("reviewer account transport is unavailable")
}

func (j *PermissionJudge) openCodeHTTPClient() *http.Client {
	if j.accountClient != nil {
		return j.accountClient()
	}
	return j.httpClient
}

// reviewerRun freezes the native account and model for execution and cleanup.
// Helper tracking and concurrency limits remain on the owning judge.
type reviewerRun struct {
	*PermissionJudge
	port string
}

func (j *PermissionJudge) snapshotReviewerRun(directory string) reviewerRun {
	var run reviewerRun
	if j.reviewerRun != nil {
		run = j.reviewerRun(directory)
	} else {
		run = reviewerRun{PermissionJudge: &PermissionJudge{
			httpClient: j.openCodeHTTPClient(), sessionIdentity: j.sessionIdentity,
		}, port: j.openCodePort(directory)}
	}
	run.modelProvider, run.modelID = j.model()
	return run
}

func (s *Service) snapshotReviewer(directory string) reviewerRun {
	adapter := s.reviewerAdapter()
	run := reviewerRun{PermissionJudge: &PermissionJudge{
		httpClient: &http.Client{Transport: unavailableReviewerTransport{}},
		sessionIdentity: func(ctx context.Context, native string) (string, error) {
			return reviewerSessionIdentity(ctx, adapter, native)
		},
	}}
	if p, ok := adapter.(platforms.OpenCodeEventClientProvider); ok {
		if client := p.OpenCodeEventClient(); client != nil {
			run.httpClient = client
		}
	}
	if p, ok := adapter.(platforms.InstancePortProvider); ok {
		if ports := p.InstancePorts(); ports != nil {
			run.port = ports.DiscoverOpenCodePort(directory)
		}
	}
	return run
}
