package gitexec

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestStream(t *testing.T) {
	ctx := context.Background()
	var got []byte
	if err := Command(ctx, "--version").Stream(func(r io.Reader) (err error) {
		got, err = io.ReadAll(r)
		return err
	}); err != nil || len(got) == 0 {
		t.Fatalf("full read: %q, %v", got, err)
	}
	if err := Command(ctx, "--version").Stream(func(io.Reader) error { return ErrStopStream }); err != nil {
		t.Fatalf("early stop err = %v, want nil", err)
	}
	boom := errors.New("boom")
	if err := Command(ctx, "--version").Stream(func(io.Reader) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("reader err = %v, want boom", err)
	}
	if err := Command(ctx, "no-such-subcommand").Stream(func(r io.Reader) error {
		_, err := io.ReadAll(r)
		return err
	}); err == nil {
		t.Fatal("git failure was swallowed")
	}
}
