package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

func TestMCPCreateRecordsCreatorBeforeSending(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		for _, failSend := range []bool{false, true} {
			t.Run(fmt.Sprintf("worktree=%v/sendFailure=%v", worktree, failSend), func(t *testing.T) {
				srv, reg := newSessionsTestServer(t)
				host := &mcpCreateHost{ensureHost: &ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
					return &hostsvc.EnsureRuntimeResult{}, nil
				}}}
				srv.hostRouter = hostsvc.NewRouter(host)
				reg.Register(&fakePlatform{PlatformID: "opencode",
					SessionFn: func(id string) (*platforms.SessionDetail, error) {
						return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/source/project"}}, nil
					},
					CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
						return &platforms.CreateSessionResponse{ID: "ses-new"}, nil
					},
					SendMessageFn: func(platforms.SendMessageRequest) error {
						var platform, id string
						err := srv.stateDB.SQL().QueryRowContext(t.Context(), `SELECT creator_platform, creator_session_id FROM session_creator WHERE platform='opencode' AND session_id='ses-new'`).Scan(&platform, &id)
						if err != nil || platform != "opencode" || id != "caller" {
							t.Errorf("creator before first send = %q/%q, %v", platform, id, err)
						}
						if failSend {
							return errors.New("send failed")
						}
						return nil
					},
				})
				got, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{
					Platform: "opencode", SessionID: "caller", Directory: "/another/project", Model: "p/model", Prompt: "go", Worktree: &worktree,
				})
				if (err != nil) != failSend || got.SessionID != "ses-new" {
					t.Fatalf("create = %#v, %v", got, err)
				}
			})
		}
	}
}

func TestMCPCreateCreatorValidationAndStorageFailure(t *testing.T) {
	for _, scenario := range []string{"missing caller", "no caller", "storage failure", "create failure"} {
		t.Run(scenario, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			srv.hostRouter = hostsvc.NewRouter(&mcpCreateHost{ensureHost: &ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				return &hostsvc.EnsureRuntimeResult{}, nil
			}}})
			created, sent := false, false
			reg.Register(&fakePlatform{PlatformID: "opencode",
				SessionFn: func(id string) (*platforms.SessionDetail, error) {
					if scenario == "missing caller" {
						return nil, platforms.ErrNotFound
					}
					return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "opencode", Directory: "/source"}}, nil
				},
				CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					created = true
					if scenario == "create failure" {
						return nil, errors.New("create failed")
					}
					if scenario == "storage failure" {
						// Simulate a write failure after the external session exists.
						if _, err := srv.stateDB.SQL().Exec(`DROP TABLE session_creator`); err != nil {
							t.Fatal(err)
						}
					}
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				},
				SendMessageFn: func(platforms.SendMessageRequest) error { sent = true; return nil },
			})
			caller := "parent"
			if scenario == "no caller" {
				caller = ""
			}
			worktree := false
			got, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{
				Platform: "opencode", SessionID: caller, Directory: "/another/project", Model: "p/model", Prompt: "go", Worktree: &worktree,
			})
			if scenario == "no caller" {
				if err != nil || !sent {
					t.Fatalf("callerless create = %#v, %v, sent=%v", got, err, sent)
				}
				link, err := srv.stateDB.GetSessionCreator(t.Context(), got.Platform, got.SessionID)
				if err != nil || link != nil {
					t.Fatalf("invented creator = %#v, %v", link, err)
				}
				return
			}
			if err == nil || sent || (scenario == "missing caller" && created) {
				t.Fatalf("create = %#v, %v, created=%v, sent=%v", got, err, created, sent)
			}
			if scenario == "storage failure" && got.SessionID != "child" {
				t.Fatal("lost partially created session identity")
			}
		})
	}
}
