package plugins

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type received struct {
	message Envelope
	err     error
	size    int
}

// serve serializes stream state on one goroutine. Dedicated pipe workers keep
// blocked writes and partial stdout frames from blocking cancellation/shutdown.
func (p *Process) serve(attempt int) error {
	if err := p.approvedExecutable(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token := p.launchToken()
	cmd := exec.Command(p.config.Candidate.Path, string(ModeServe))
	cmd.Dir = p.config.DataDir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "OCMAN_PLUGIN_TOKEN=" + token}
	// An unlinked 0600 file avoids pipe backpressure when an older plugin ignores
	// configuration. The child reads fd 3 before announcing readiness.
	config, err := os.CreateTemp("", "ocman-plugin-config-*")
	if err != nil {
		return ErrUnavailable
	}
	defer config.Close()
	if err := os.Remove(config.Name()); err != nil {
		return ErrUnavailable
	}
	if _, err := config.Write(append(append([]byte(nil), p.config.Configuration...), '\n')); err != nil {
		return ErrUnavailable
	}
	if _, err := config.Seek(0, io.SeekStart); err != nil {
		return ErrUnavailable
	}
	cmd.ExtraFiles = []*os.File{config}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ErrUnavailable
	}
	defer stdin.Close()
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return ErrUnavailable
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrCapture{p}
	if err := cmd.Start(); err != nil {
		return ErrUnavailable
	}
	_ = config.Close()
	_ = stdoutWriter.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = stdin.Close()
		_ = stdout.Close()
		<-exited
	}()
	writes := make(chan Envelope, MaxConcurrency+2)
	written := make(chan received, MaxConcurrency+2)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		encoder := NewEncoder(stdin)
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-writes:
				err := encoder.Encode(e)
				select {
				case written <- received{message: e, err: err}:
				case <-ctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}
	}()
	frames := make(chan received)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		decoder := NewDecoder(stdout)
		for {
			e, err := decoder.Decode()
			select {
			case frames <- received{message: e, err: err, size: decoder.size}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		<-workerDone
		<-readerDone
	}()
	stream, _ := NewStream(ModeServe, token, Version{ProtocolMajor, ProtocolMinor}, p.config.Supported)
	send := func(e Envelope) error {
		if err := stream.Accept(FromHost, e); err != nil {
			return err
		}
		select {
		case writes <- e:
			return nil
		default:
			return ErrOutputLimit
		}
	}
	pending := make(map[uint64]*invocation)
	defer func() {
		for _, r := range pending {
			err := invocationError(r, time.Now())
			if err == nil {
				err = ErrUnavailable
			}
			finish(r, Reply{Err: err})
		}
	}()
	ready := false
	var nextID uint64
	readiness := time.NewTimer(p.policy.ready)
	defer readiness.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	exitSignal := (<-chan struct{})(exited)
	var drain <-chan time.Time
	drainTimer := time.NewTimer(time.Hour)
	drainTimer.Stop()
	defer drainTimer.Stop()
	for {
		select {
		case <-p.ctx.Done():
			for _, r := range pending {
				finish(r, Reply{Err: ErrUnavailable})
			}
			if ready {
				_ = send(Envelope{Type: TypeShutdown, Shutdown: &Shutdown{}})
			}
			timer := time.NewTimer(p.policy.grace)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
			}
			return ErrUnavailable
		case <-exitSignal:
			exitSignal = nil
			drainTimer.Reset(100 * time.Millisecond)
			drain = drainTimer.C
		case <-drain:
			return ErrUnavailable
		case <-readiness.C:
			return ErrHandshake
		case write := <-written:
			if write.err != nil {
				return ErrUnavailable
			}
			if write.message.Type == TypeHello {
				ready = true
				readiness.Stop()
				p.setHealth("ready", attempt, nil)
			}
		case r := <-p.calls:
			if !ready || p.ctx.Err() != nil {
				finish(r, Reply{Err: ErrUnavailable})
				continue
			}
			if len(pending) >= stream.limit {
				finish(r, Reply{Err: ErrBusy})
				continue
			}
			if err := r.ctx.Err(); err != nil {
				finish(r, Reply{Err: err})
				continue
			}
			if time.Now().UnixMilli() >= r.call.DeadlineUnixMS {
				finish(r, Reply{Err: context.DeadlineExceeded})
				continue
			}
			if !stream.hasCapabilityVersion(r.call.Capability, r.call.Version) {
				finish(r, Reply{Err: ErrInvalidMessage})
				continue
			}
			nextID++
			r.call.ID = nextID
			pending[nextID] = r
			if err := send(Envelope{Type: TypeCall, Call: &r.call}); err != nil {
				return err
			}
		case frame := <-frames:
			if frame.err != nil {
				if errors.Is(frame.err, io.EOF) {
					return ErrUnavailable
				}
				if errors.Is(frame.err, ErrMessageTooLarge) {
					return ErrMessageTooLarge
				}
				return ErrInvalidMessage
			}
			e := frame.message
			if err := stream.Accept(FromPlugin, e); err != nil {
				return err
			}
			switch e.Type {
			case TypeHello:
				if !p.matchesOffer(e) {
					return ErrIdentityChanged
				}
				n, _ := stream.Negotiation()
				if err := send(Envelope{Type: TypeHello, Hello: &Hello{Mode: ModeServe, Accepted: &n}}); err != nil {
					return err
				}
			case TypeEvent:
				select {
				case p.events <- *e.Event:
				default:
					return ErrOutputLimit
				}
			case TypeChunk, TypeResult:
				var id uint64
				if e.Chunk != nil {
					id = e.Chunk.ID
				} else {
					id = e.Result.ID
				}
				r := pending[id]
				if r.replies != nil {
					err := invocationError(r, time.Now())
					if err != nil {
						finish(r, Reply{Err: err})
					}
				}
				r.bytes += frame.size
				if r.bytes > MaxCallOutputBytes {
					return ErrOutputLimit
				}
				if e.Result != nil {
					finish(r, Reply{Message: e})
					delete(pending, id)
				} else if r.replies != nil {
					if len(r.replies) >= maxBufferedFrames {
						return ErrOutputLimit
					}
					r.replies <- Reply{Message: e}
				}
			}
		case <-tick.C:
			now := time.Now()
			terminate := false
			for id, r := range pending {
				if !r.cancelled.IsZero() {
					if now.Sub(r.cancelled) >= p.policy.grace {
						terminate = true
					}
					continue
				}
				err := invocationError(r, now)
				if err != nil {
					finish(r, Reply{Err: err})
					r.cancelled = now
					if err := send(Envelope{Type: TypeCancel, Cancel: &Cancel{ID: id}}); err != nil {
						return err
					}
				}
			}
			if terminate {
				return ErrUnavailable
			}
		}
	}
}
