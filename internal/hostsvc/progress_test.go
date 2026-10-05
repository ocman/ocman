package hostsvc

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestProgress(t *testing.T) {
	ReportProgress(context.Background(), StepSession, StepActive) // no reporter: no-op
	var got []string
	ctx := WithProgress(context.Background(), func(step, state string) { got = append(got, step+":"+state) })
	ReportProgress(ctx, StepSession, StepActive)
	FinishStep(ctx, StepSession, nil)
	FinishStep(ctx, StepPrompt, errors.New("x"))
	want := []string{"session:active", "session:done", "prompt:error"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
