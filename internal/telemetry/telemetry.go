// Package telemetry owns ocman's OpenTelemetry lifecycle: parsing the
// --otel endpoint, choosing an OTLP transport, building the trace and
// metric providers, and installing them as the global OTel providers
// so the rest of the codebase can stay configuration-free.
//
// When the endpoint is empty, Init is a no-op and returns a no-op
// shutdown func. The OTel SDK's default global providers are tracing
// and metric no-ops, so every otelhttp/otelsql/manual-tracer call site
// stays cheap (a handful of nil-typed method dispatches per call) when
// telemetry is disabled.
//
// Configuration philosophy: the --otel flag only specifies *where* to
// send telemetry. Everything else (service name, sampling, headers,
// resource attributes) is read from the standard OTEL_* environment
// variables per the OTel spec. This keeps ocman's CLI surface tiny
// while still allowing operators to tune the deployment from outside.
package telemetry
