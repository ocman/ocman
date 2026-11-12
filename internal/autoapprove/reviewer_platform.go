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
