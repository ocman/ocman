package usageforward

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type recordsKey struct{}

// The SDK creates records with their resource/scope attached. Capture them in
// this call's context, then Export directly so HTTP acknowledgement reflects
// the actual OTLP result instead of an asynchronous logger queue admission.
type captureProcessor struct{}

func (captureProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (captureProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
	if records, ok := ctx.Value(recordsKey{}).(*[]sdklog.Record); ok {
		*records = append(*records, record.Clone())
	}
	return nil
}
func (captureProcessor) Shutdown(context.Context) error   { return nil }
func (captureProcessor) ForceFlush(context.Context) error { return nil }
