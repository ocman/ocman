package local

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
)

const serviceURL = "http://127.0.0.1:49374"

// endpointRuntime scripts Probe per endpoint; launched servers use
// fakeRuntime's endpoint and are healthy unless scripted otherwise.
type endpointRuntime struct {
	fakeRuntime
	pmu    sync.Mutex
	probes map[string]error
}

func (r *endpointRuntime) Probe(_ context.Context, inst *ocruntime.Instance) error {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	return r.probes[inst.Endpoint]
}

func (r *endpointRuntime) setProbe(endpoint string, err error) {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	r.probes[endpoint] = err
}

type serviceHost struct {
	*Host
	rt    *endpointRuntime
	store *fakeStore
	rec   *portRecorder
	mu    sync.Mutex
	svc   *ocv2.Service
}

func (s *serviceHost) setService(svc *ocv2.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.svc = svc
}

func newServiceHost(t *testing.T) *serviceHost {
	t.Helper()
	t.Cleanup(ocv2.SetInstalledV2(true))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	s := &serviceHost{
		rt:    &endpointRuntime{fakeRuntime: fakeRuntime{endpoint: "http://127.0.0.1:7777"}, probes: map[string]error{}},
		store: newFakeStore(),
		rec:   &portRecorder{},
		svc:   &ocv2.Service{URL: serviceURL, PID: 4242, Version: "2.0.31"},
	}
	s.Host = New(Deps{Runtime: s.rt, ManagedStore: s.store, SetMachineServer: s.rec.set, OpenCodeService: func() (ocv2.Service, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.svc == nil {
			return ocv2.Service{}, ocv2.ErrNoService
		}
		return *s.svc, nil
	}})
	s.portWaitTimeout = 50 * time.Millisecond
	s.portWaitInterval = 5 * time.Millisecond
	return s
}

func (s *serviceHost) ensure(t *testing.T) (*hostsvc.EnsureRuntimeResult, error) {
	t.Helper()
	return s.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
}

func TestV2EnsureUsesRunningService(t *testing.T) {
	s := newServiceHost(t)
	res, err := s.ensure(t)
	if err != nil {
		t.Fatal(err)
	}
	if res.Endpoint != serviceURL || res.Launched || res.Runtime.Kind != ocruntime.KindService {
		t.Fatalf("result = %+v; want the background service, not a launch", res)
	}
	if s.rt.launchCount() != 0 || s.rec.last() != "49374" {
		t.Fatalf("launches=%d published=%q; want 0 and the service port", s.rt.launchCount(), s.rec.last())
	}
	if len(s.store.rows) != 0 {
		t.Fatalf("service persisted as a managed instance: %+v", s.store.rows)
	}
	if ports := s.ManagedOpenCodePorts(t.Context()); ports[machineRoot()] != "49374" {
		t.Fatalf("managed ports = %v; want the service routed", ports)
	}

	// A project archive or maintenance stop forgets it but never stops it.
	if err := s.StopProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: machineRoot()}); err != nil {
		t.Fatal(err)
	}
	if s.rt.stopCount() != 0 || s.rec.last() != "" {
		t.Fatalf("stop: stops=%d published=%q; want 0 and withdrawn", s.rt.stopCount(), s.rec.last())
	}

	if _, err := s.ensure(t); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, ErrServiceNotManaged) {
		t.Fatalf("restart err = %v; want ErrServiceNotManaged", err)
	}
	if s.rt.stopCount() != 0 || s.rt.launchCount() != 0 || s.rec.last() != "49374" {
		t.Fatalf("restart touched the service: stops=%d launches=%d published=%q", s.rt.stopCount(), s.rt.launchCount(), s.rec.last())
	}
}

func TestV2BusyServiceIsAwaitedNotDuplicated(t *testing.T) {
	s := newServiceHost(t)
	s.rt.setProbe(serviceURL, context.DeadlineExceeded) // migrating
	if _, err := s.ensure(t); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want the inconclusive probe", err)
	}
	if _, err := s.ensure(t); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second err = %v; want the inconclusive probe", err)
	}
	if s.rt.launchCount() != 0 || s.rt.stopCount() != 0 {
		t.Fatalf("launches=%d stops=%d while the service was busy; want 0/0", s.rt.launchCount(), s.rt.stopCount())
	}
	if s.rec.last() == "49374" {
		t.Fatal("busy service published before it answered")
	}
	s.rt.setProbe(serviceURL, nil)
	if res, err := s.ensure(t); err != nil || res.Endpoint != serviceURL || s.rec.last() != "49374" {
		t.Fatalf("ready service: res=%+v err=%v published=%q", res, err, s.rec.last())
	}
}

func TestV2ServiceAuthenticationFailureNeverLaunches(t *testing.T) {
	s := newServiceHost(t)
	s.rt.setProbe(serviceURL, ocapi.ErrAuthentication)
	for range 2 {
		res, err := s.ensure(t)
		if !errors.Is(err, ocapi.ErrAuthentication) || res != nil {
			t.Fatalf("res=%+v err=%v; want the authentication error", res, err)
		}
	}
	if s.rt.launchCount() != 0 || s.rt.stopCount() != 0 || s.rec.last() != "" || len(s.store.rows) != 0 {
		t.Fatalf("launches=%d stops=%d published=%q rows=%v; want no launch, stop, routing or persistence",
			s.rt.launchCount(), s.rt.stopCount(), s.rec.last(), s.store.rows)
	}
	// Correcting credentials makes the same service usable on the next ensure.
	s.rt.setProbe(serviceURL, nil)
	if res, err := s.ensure(t); err != nil || res.Endpoint != serviceURL || s.rt.launchCount() != 0 {
		t.Fatalf("res=%+v err=%v launches=%d; want the service after auth recovers", res, err, s.rt.launchCount())
	}
}

func TestV2NoServiceLaunchesOwnServer(t *testing.T) {
	s := newServiceHost(t)
	s.setService(nil)
	if res, err := s.ensure(t); err != nil || !res.Launched {
		t.Fatalf("res=%+v err=%v; want a launch", res, err)
	}
	// A service that appears later does not displace a healthy own server.
	s.setService(&ocv2.Service{URL: serviceURL, PID: 4242, Version: "2.0.31"})
	if res, err := s.ensure(t); err != nil || res.Endpoint != "http://127.0.0.1:7777" || s.rt.launchCount() != 1 {
		t.Fatalf("res=%+v err=%v launches=%d; want the own server kept", res, err, s.rt.launchCount())
	}
}

func TestV2UnreachableRegisteredServiceFallsBackToLaunch(t *testing.T) {
	s := newServiceHost(t)
	s.rt.setProbe(serviceURL, ocruntime.ErrProbeUnreachable)
	res, err := s.ensure(t)
	if err != nil || !res.Launched || res.Endpoint != "http://127.0.0.1:7777" {
		t.Fatalf("res=%+v err=%v; want a launch for the unreachable service", res, err)
	}
}

func TestV2DeadServiceIsForgottenNotStopped(t *testing.T) {
	s := newServiceHost(t)
	if _, err := s.ensure(t); err != nil {
		t.Fatal(err)
	}
	s.rt.setProbe(serviceURL, ocruntime.ErrProbeUnreachable)
	s.setService(nil)
	res, err := s.ensure(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.rt.stopCount() != 0 || !res.Launched || s.rec.last() != "7777" {
		t.Fatalf("stops=%d res=%+v published=%q; want the service left alone and a launch", s.rt.stopCount(), res, s.rec.last())
	}
}

func TestV2RestartedServiceIsRediscovered(t *testing.T) {
	s := newServiceHost(t)
	if _, err := s.ensure(t); err != nil {
		t.Fatal(err)
	}
	const moved = "http://127.0.0.1:50000"
	s.rt.setProbe(serviceURL, ocruntime.ErrProbeUnreachable)
	s.setService(&ocv2.Service{URL: moved, PID: 4343, Version: "2.0.31"})
	res, err := s.ensure(t)
	if err != nil || res.Endpoint != moved || s.rt.launchCount() != 0 || s.rec.last() != "50000" {
		t.Fatalf("res=%+v err=%v launches=%d published=%q; want the moved service", res, err, s.rt.launchCount(), s.rec.last())
	}
}

func TestV2ServiceIgnoredOutsideMachineRootOrV1(t *testing.T) {
	s := newServiceHost(t)
	if res, handled, err := s.useService(t.Context(), "/some/repo"); handled || res != nil || err != nil {
		t.Fatalf("project root used the service: %+v %v %v", res, handled, err)
	}
	ocv2.SetInstalledV2(false)
	if _, handled, _ := s.useService(t.Context(), machineRoot()); handled {
		t.Fatal("v1 used the v2 service")
	}
}

func TestV2ServiceReadErrorFallsBackToLaunch(t *testing.T) {
	s := newServiceHost(t)
	s.deps.OpenCodeService = func() (ocv2.Service, error) { return ocv2.Service{}, errors.New("permission denied") }
	if res, err := s.ensure(t); err != nil || !res.Launched {
		t.Fatalf("res=%+v err=%v; want a launch", res, err)
	}
}

// A launch that is alive but slow (a first start migrating the database)
// must not be killed and replaced: that started two servers on one
// database, which then locked each other out.
func TestLaunchStillStartingIsKeptNotStopped(t *testing.T) {
	s := newServiceHost(t)
	s.setService(nil)
	s.rt.setProbe("http://127.0.0.1:7777", context.DeadlineExceeded)
	if _, err := s.ensure(t); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want the inconclusive probe", err)
	}
	if s.rt.stopCount() != 0 || !s.store.has(machineRoot()) || s.rec.last() == "7777" {
		t.Fatalf("stops=%d persisted=%v published=%q; want kept, persisted, unpublished", s.rt.stopCount(), s.store.has(machineRoot()), s.rec.last())
	}
	if _, err := s.ensure(t); !errors.Is(err, context.DeadlineExceeded) || s.rt.launchCount() != 1 {
		t.Fatalf("err=%v launches=%d; want the same server awaited", err, s.rt.launchCount())
	}
	s.rt.setProbe("http://127.0.0.1:7777", nil)
	if res, err := s.ensure(t); err != nil || res.Endpoint != "http://127.0.0.1:7777" || s.rt.launchCount() != 1 || s.rec.last() != "7777" {
		t.Fatalf("res=%+v err=%v launches=%d published=%q", res, err, s.rt.launchCount(), s.rec.last())
	}
}
