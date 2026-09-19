package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// Handler must honor ctx and stop using emit before returning. Calls may run
// concurrently up to Description.MaxConcurrency. OperationID is available for
// application-owned deduplication; the SDK never retries a call.
type Handler func(ctx context.Context, call Call, emit func(json.RawMessage) error) (json.RawMessage, error)

// Failure returns a safe wire category. All other errors become internal.
type Failure ErrorCategory

func (f Failure) Error() string { return string(f) }

func normalizedError(err error) *WireError {
	category := ErrorInternal
	var f Failure
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		category = ErrorDeadlineExceeded
	case errors.Is(err, context.Canceled):
		category = ErrorCancelled
	case errors.As(err, &f):
		candidate := Result{ID: 1, Error: &WireError{Category: ErrorCategory(f)}}
		if (Envelope{Type: TypeResult, Result: &candidate}).Validate() == nil {
			category = ErrorCategory(f)
		}
	}
	return &WireError{Category: category}
}

// Run serves one launch. It owns and closes both pipes on return or cancellation;
// their Close methods must unblock pending I/O (as os.File and net.Conn do).
// Describe emits one hello and exits without invoking handler. Serve waits for
// negotiation before dispatching. Shutdown cancels work and emits no more frames.
func Run(ctx context.Context, mode Mode, token string, description Description, input io.ReadCloser, output io.WriteCloser, handler Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	closed := make(chan struct{})
	defer close(closed)
	defer input.Close()
	defer output.Close()
	go func() {
		select {
		case <-ctx.Done():
			_ = input.Close()
			_ = output.Close()
		case <-closed:
		}
	}()
	hello := Envelope{Type: TypeHello, Hello: &Hello{Mode: mode, Token: token, Description: &description}}
	encoder, decoder := NewEncoder(output), NewDecoder(input)
	if err := encoder.Encode(hello); err != nil {
		return err
	}
	if mode == ModeDescribe {
		return nil
	}
	ack, err := decoder.Decode()
	if err != nil {
		return err
	}
	if ack.Hello == nil || ack.Hello.Accepted == nil {
		return ErrHandshake
	}
	accepted := ack.Hello.Accepted
	stream, err := NewStream(mode, token, accepted.Protocol, accepted.Capabilities)
	if err != nil {
		return err
	}
	if err = stream.Accept(FromPlugin, hello); err != nil {
		return err
	}
	if err = stream.Accept(FromHost, ack); err != nil {
		return err
	}
	type incoming struct {
		message Envelope
		err     error
	}
	reads := make(chan incoming)
	writes := make(chan Envelope)
	go func() {
		for {
			e, err := decoder.Decode()
			select {
			case reads <- incoming{e, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	active := map[uint64]context.CancelFunc{}
	defer func() {
		for _, stop := range active {
			stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case in := <-reads:
			if in.err != nil {
				return in.err
			}
			e := in.message
			if err := stream.Accept(FromHost, e); err != nil {
				return err
			}
			switch e.Type {
			case TypeShutdown:
				return nil
			case TypeCancel:
				active[e.Cancel.ID]()
			case TypeCall:
				callCtx, stop := context.WithDeadline(ctx, time.UnixMilli(e.Call.DeadlineUnixMS))
				active[e.Call.ID] = stop
				go dispatch(ctx, callCtx, *e.Call, handler, writes)
			}
		case e := <-writes:
			if err := stream.Accept(FromPlugin, e); err != nil {
				return err
			}
			if err := encoder.Encode(e); err != nil {
				return err
			}
			if e.Result != nil {
				active[e.Result.ID]()
				delete(active, e.Result.ID)
			}
		}
	}
}

func dispatch(ctx, callCtx context.Context, call Call, handler Handler, writes chan<- Envelope) {
	var sequence uint64
	emit := func(data json.RawMessage) error {
		if err := callCtx.Err(); err != nil {
			return err
		}
		e := Envelope{Type: TypeChunk, Chunk: &Chunk{ID: call.ID, Sequence: sequence + 1, Data: append(json.RawMessage(nil), data...)}}
		if err := NewEncoder(io.Discard).Encode(e); err != nil {
			return err
		}
		select {
		case writes <- e:
			sequence++
			return nil
		case <-callCtx.Done():
			return callCtx.Err()
		}
	}
	var value json.RawMessage
	err := callCtx.Err()
	if err == nil {
		if handler == nil {
			err = Failure(ErrorNotFound)
		} else {
			value, err = handler(callCtx, call, emit)
		}
	}
	if callCtx.Err() != nil {
		err = callCtx.Err()
	}
	result := &Result{ID: call.ID, Value: value}
	if err == nil && NewEncoder(io.Discard).Encode(Envelope{Type: TypeResult, Result: result}) != nil {
		err = Failure(ErrorInternal)
	}
	if err != nil {
		result.Value, result.Error = nil, normalizedError(err)
	}
	select {
	case writes <- Envelope{Type: TypeResult, Result: result}:
	case <-ctx.Done():
	}
}
