package sessionlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

type startSessions struct {
	Sessions
	create func(platforms.CreateSessionRequest, []platforms.PermissionRule) (*platforms.CreateSessionResponse, error)
}

func (*startSessions) KnownPlatform(id string) bool { return id == "r-box:claudecode" }
func (*startSessions) ResolvePlatformID(_ context.Context, id string) (string, error) {
	return id, nil
}
func (s *startSessions) CreateConfigured(_ context.Context, _ string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	return s.create(req, rules)
}

type startHost struct {
	hostsvc.Host
	steps *[]string
	rules []platforms.PermissionRule
}

func (h *startHost) EnsureRuntime(_ context.Context, backend, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	*h.steps = append(*h.steps, "ensure:"+backend+":"+dir)
	return &hostsvc.EnsureRuntimeResult{}, nil
}
func (h *startHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	if req.Platform != "claudecode" || !req.AutoName || req.Prompt != "first" || !reflect.DeepEqual(req.PermissionRules, h.rules) {
		return nil, errors.New("incorrect worktree request")
	}
	*h.steps = append(*h.steps, "worktree")
	return &hostsvc.WorktreeSessionResult{SessionID: "child", WorktreePath: "/worktree", Branch: "session-child"}, nil
}

// Creation must apply the original posture before sending. A failed first
// delivery returns the created child and its retry receipt, never a second send.
func TestStartFirstDeliveryOrderingAndFailureReceipt(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "checkout", true: "worktree"}[worktree], map[bool]string{false: "sent", true: "failed"}[fail]}, "/"), func(t *testing.T) {
				var steps, progress []string
				rules := []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}
				host := &startHost{steps: &steps, rules: rules}
				svc := &startSessions{create: func(req platforms.CreateSessionRequest, got []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
					if req.Directory != "/repo" || !reflect.DeepEqual(got, rules) {
						t.Fatal("creation lost permission defaults or directory")
					}
					steps = append(steps, "create")
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				}}
				m := NewModule(Deps{
					Sessions: func() Sessions { return svc },
					Owner: func(w http.ResponseWriter, dir, owner string) (hostsvc.Host, bool) {
						if owner != "box" || dir != "/repo" {
							t.Fatalf("owner=%q dir=%q", owner, dir)
						}
						return host, true
					},
					CreatePlatform: func(_ context.Context, got hostsvc.Host, id, model string) (string, string, error) {
						if got != host || id != "r-box:claudecode" || model != "claudecode:acme/m" {
							t.Fatal("incorrect backend selection")
						}
						return id, model, nil
					},
					PermissionDefaults: func(_ context.Context, dir, owner string) ([]platforms.PermissionRule, error) {
						steps = append(steps, "defaults")
						return rules, nil
					},
					SendNow: func(_ context.Context, platform string, req platforms.SendMessageRequest) error {
						steps = append(steps, "send")
						if platform != "r-box:claudecode" || req.SessionID != "child" || req.Message != "first" || req.Model != "claudecode:acme/m" || req.Agent != "plan" || req.Reasoning != "high" || !reflect.DeepEqual(req.Images, []platforms.ImageAttachment{{URL: "data:image/png;base64,a", Mime: "image/png"}}) {
							t.Fatalf("first payload changed: %+v", req)
						}
						if fail {
							return errors.New("first delivery failed")
						}
						return nil
					},
					Broadcast: func(event string, data []byte) {
						var receipt struct{ StartID, Step, State string }
						if event != "ocman.session.start.progress" || json.Unmarshal(data, &receipt) != nil || receipt.StartID != "start-1" {
							t.Fatalf("invalid progress: %s %s", event, data)
						}
						progress = append(progress, receipt.Step+":"+receipt.State)
					},
				})
				body := `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","prompt":"first","startId":"start-1","send":{"message":"first","model":"claudecode:acme/m","agent":"plan","reasoning":"high","images":[{"url":"data:image/png;base64,a","mime":"image/png"}]},"worktree":` + map[bool]string{true: "true", false: "false"}[worktree] + `}`
				w := httptest.NewRecorder()
				m.HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(body)))
				var response struct {
					SessionID, Platform, RemoteID, Directory, FirstMessageError string
					FirstMessageSent                                            bool
				}
				if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != http.StatusOK || response.SessionID != "child" || response.Platform != "r-box:claudecode" || response.RemoteID != "box" || response.FirstMessageSent == fail {
					t.Fatalf("start receipt: %d %s", w.Code, w.Body.String())
				}
				wantSteps := []string{"defaults", "ensure:claudecode:/repo", "create", "send"}
				wantDir := "/repo"
				if worktree {
					wantSteps, wantDir = []string{"defaults", "worktree", "send"}, "/worktree"
				}
				if !reflect.DeepEqual(steps, wantSteps) || response.Directory != wantDir {
					t.Fatalf("steps=%v directory=%s", steps, response.Directory)
				}
				wantFinal := "prompt:done"
				if fail {
					wantFinal = "prompt:error"
				}
				if progress[len(progress)-1] != wantFinal || fail && response.FirstMessageError != "first delivery failed" {
					t.Fatalf("progress=%v receipt=%+v", progress, response)
				}
			})
		}
	}
}
