package server

import (
	"context"
	"github.com/ocman/ocman/internal/hostsvc"
)

func (h *factoryPlanningHost) RemoteID() string { return "local" }

func (h *ensureHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *ownerHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *ownerSpy) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *draftOwnerHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *otherOwnerHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *factoryPlanningHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *factoryImplementationHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *projectHandleRemoteHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *routineTestHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
func (h *conversationTestHost) EnsureRuntime(ctx context.Context, _ string, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}
