package remote

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSessionChangeStreamQualifiesOwnerAndReconcilesOnAttachment(t *testing.T) {
	srv := NewServer(platforms.NewRegistry(), nil, "machine", "test").UseSessionChanges(func(_ context.Context, send func(string, string) error) error {
		for _, change := range [][2]string{{}, {"other", "s1"}, {"r-evil:fake", "s1"}, {"fake", ""}, {"fake", "s1"}, {"fake", "s2"}} {
			if err := send(change[0], change[1]); err != nil {
				return err
			}
		}
		return nil
	})
	conn := &RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", srv)), remoteID: "machine"}
	p := newRemotePlatform(conn, "fake", nil)
	p.lastSess = []db.Session{{ID: "s0"}}
	r := platforms.NewRegistry()
	var seen []string
	r.SetSessionEventSink(platforms.SessionEventSink{OnSessionBackgroundChanged: func(platform, id string) {
		if platform != "r-machine:fake" {
			t.Errorf("untrusted owner identity: %s", platform)
		}
		seen = append(seen, id)
		if id == "s1" {
			r.Unregister(p.ID())
		}
	}})
	r.Register(p)
	m := &Manager{registry: r}
	err := m.receiveSessionChanges(t.Context(), &managedRemote{conn: conn}, []*remotePlatform{p})
	if !errors.Is(err, io.EOF) || !reflect.DeepEqual(seen, []string{"s0", "s1"}) {
		t.Fatalf("events=%v err=%v", seen, err)
	}
}

func TestSessionChangeStreamUnavailableAndCanonicalReferences(t *testing.T) {
	m := &Manager{}
	if err := m.receiveSessionChanges(t.Context(), &managedRemote{conn: &RemoteConn{}}, nil); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	srv := NewServer(platforms.NewRegistry(), nil, "machine", "test")
	for _, token := range []string{"token", "wrong"} {
		conn := &RemoteConn{client: pb.NewOcmanClient(startTestServerAs(t, "token", token, srv))}
		err := m.receiveSessionChanges(t.Context(), &managedRemote{conn: conn}, nil)
		want := codes.Unimplemented
		if token == "wrong" {
			want = codes.Unauthenticated
		}
		if status.Code(err) != want {
			t.Fatalf("token=%s: %v", token, err)
		}
	}
	p := newRemotePlatform(&RemoteConn{protocolVersion: canonicalSessionProtocol, remoteID: "machine"}, "fake", nil)
	p.publishSessionChange("ocm_c69e5bc8-4216-41a9-842d-1158f8df9eb4") // no sink
	var seen []string
	p.SetSessionEventSink(platforms.SessionEventSink{OnSessionBackgroundChanged: func(_, id string) { seen = append(seen, id) }})
	for _, id := range []string{"", "native-id", "ocm_c69e5bc8-4216-41a9-842d-1158f8df9eb4"} {
		p.publishSessionChange(id)
	}
	if len(seen) != 1 {
		t.Fatalf("canonical invalidations=%v", seen)
	}
}

func TestSessionChangeStreamRetriesAndJoinsAtShutdown(t *testing.T) {
	oldDelay := reconnectBaseDelay
	reconnectBaseDelay = time.Millisecond
	t.Cleanup(func() { reconnectBaseDelay = oldDelay })
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "retry", false: "older-peer"}[supported], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var attempts atomic.Int32
			attempted := make(chan struct{}, 1)
			srv := NewServer(platforms.NewRegistry(), nil, "machine", "test")
			if supported {
				srv.UseSessionChanges(func(ctx context.Context, send func(string, string) error) error {
					if attempts.Add(1) == 1 {
						return status.Error(codes.Unavailable, "transient stream failure")
					}
					if err := send("fake", "s1"); err != nil {
						return err
					}
					<-ctx.Done()
					return ctx.Err()
				})
			} else {
				srv.UseSessionChanges(func(context.Context, func(string, string) error) error {
					attempts.Add(1)
					attempted <- struct{}{}
					return status.Error(codes.Unimplemented, "older owner")
				})
			}
			cc := startTestServer(t, "token", srv)
			conn := &RemoteConn{conn: cc, client: pb.NewOcmanClient(cc), remoteID: "machine"}
			p := newRemotePlatform(conn, "fake", nil)
			r := platforms.NewRegistry()
			r.SetSessionEventSink(platforms.SessionEventSink{OnSessionBackgroundChanged: func(_, _ string) { cancel() }})
			r.Register(p)
			done := make(chan struct{})
			go func() {
				defer close(done)
				(&Manager{registry: r}).waitForDropWithSessionChanges(ctx, &managedRemote{conn: conn}, []*remotePlatform{p})
			}()
			if !supported {
				select {
				case <-attempted:
				case <-ctx.Done():
					t.Fatal("optional stream never attempted")
				}
				// Unimplemented must not tear down the otherwise healthy connection.
				if _, err := conn.Client().Hello(ctx, &pb.HelloReq{}); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			<-done
			if (supported && attempts.Load() != 2) || (!supported && attempts.Load() != 1) {
				t.Fatalf("stream attempts=%d", attempts.Load())
			}
		})
	}
}
