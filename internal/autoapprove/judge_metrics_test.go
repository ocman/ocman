package autoapprove

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type metricsJudge struct{ result JudgeResult }

func (j metricsJudge) JudgeWithCallback(context.Context, string, string, []string, map[string]any, []PromptSection, func(string)) JudgeResult {
	return j.result
}

type metricsStore struct {
	lifecycleStore
	err   error
	value string
}

func (s *metricsStore) GetSetting(context.Context, string) (string, bool, error) {
	return s.value, s.value != "", s.err
}

func TestSystemOneMetricsThroughEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"answers":{"verdict":{"choice":"uncertain","probabilities":{"safe":0.4,"unsafe":0.07,"uncertain":0.53}}}}`)
	}))
	defer server.Close()
	config, err := json.Marshal(JudgeEndpoint{Format: "typesafe", Endpoint: server.URL, MinSafeProbability: .5})
	if err != nil {
		t.Fatal(err)
	}
	store := &metricsStore{value: string(config)}
	s := &Service{deps: Deps{Store: store}, judge: &PermissionJudge{store: store}}
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	for range 2 {
		result := s.judgePermission(t.Context(), "opencode", "/project", "bash", nil, nil, nil, nil)
		if result.EvaluationFailed || result.scores == nil {
			t.Fatalf("result = %+v", result)
		}
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "ocman.judge.probability" {
				continue
			}
			found = true
			points := m.Data.(metricdata.Histogram[float64]).DataPoints
			if len(points) != 3 {
				t.Fatalf("points = %+v", points)
			}
			for _, point := range points {
				backend, _ := point.Attributes.Value("backend")
				if point.Count != 2 || backend.AsString() != "typesafe" {
					t.Errorf("point = %+v", point)
				}
			}
		}
	}
	if !found {
		t.Fatal("endpoint did not emit confidence metrics")
	}
}

func TestJudgeMetrics(t *testing.T) {
	for _, tc := range []struct {
		name, backend, outcome string
		judge                  Judge
		storeErr               error
		scores                 bool
	}{
		{"safe", "custom", "approved", metricsJudge{result: JudgeResult{Verdict: verdictSafe}}, nil, false},
		{"unsafe", "custom", "not_approved", metricsJudge{result: JudgeResult{Verdict: verdictUnsafe}}, nil, false},
		{"error", "custom", "error", metricsJudge{result: endpointFailure("failed")}, nil, false},
		{"panic", "custom", "error", panickingJudge{}, nil, false},
		{"unavailable", "unavailable", "needs_user", nil, nil, false},
		{"opencode unavailable", "opencode", "needs_user", (*PermissionJudge)(nil), nil, false},
		{"settings error", "unavailable", "error", nil, errors.New("offline"), false},
		{"systemone uncertain", "custom", "not_approved", metricsJudge{result: parseTypeSafeVerdict([]byte(`{"answers":{"verdict":{"choice":"uncertain","probabilities":{"safe":0.4,"unsafe":0.07,"uncertain":0.53}}}}`), .5)}, nil, true},
		{"systemone safe", "custom", "approved", metricsJudge{result: parseTypeSafeVerdict([]byte(`{"answers":{"verdict":{"choice":"safe","probabilities":{"safe":0.99,"unsafe":0.005,"uncertain":0.005}}}}`), .95)}, nil, true},
		{"invalid scores", "custom", "error", metricsJudge{result: parseTypeSafeVerdict([]byte(`{"answers":{"verdict":{"choice":"safe","probabilities":{"safe":0.9,"unsafe":0.2,"uncertain":0.1}}}}`), .5)}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			previous := otel.GetMeterProvider()
			otel.SetMeterProvider(provider)
			t.Cleanup(func() {
				otel.SetMeterProvider(previous)
				_ = provider.Shutdown(context.Background())
			})
			s := &Service{deps: Deps{Store: &metricsStore{err: tc.storeErr}, SelectJudge: func(platforms.ID, string) Judge { return tc.judge }}}
			result := s.judgePermission(t.Context(), "private-owner", "/private/path", "bash", []string{"secret-command"}, nil, nil, nil)
			var data metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &data); err != nil {
				t.Fatal(err)
			}
			metrics := map[string]metricdata.Metrics{}
			for _, scope := range data.ScopeMetrics {
				for _, m := range scope.Metrics {
					if strings.HasPrefix(m.Name, "ocman.judge.") {
						metrics[m.Name] = m
					}
				}
			}
			calls := metrics["ocman.judge.calls"].Data.(metricdata.Sum[int64]).DataPoints
			if len(calls) != 1 || calls[0].Value != 1 || calls[0].Attributes.Len() != 2 {
				t.Fatalf("calls = %+v", calls)
			}
			for key, want := range map[string]string{"backend": tc.backend, "outcome": tc.outcome} {
				got, _ := calls[0].Attributes.Value(attribute.Key(key))
				if got.AsString() != want {
					t.Errorf("%s = %s, want %s", key, got.AsString(), want)
				}
			}
			duration := metrics["ocman.judge.duration"].Data.(metricdata.Histogram[float64]).DataPoints
			if len(duration) != 1 || duration[0].Count != 1 || duration[0].Sum < 0 || !duration[0].Attributes.Equals(&calls[0].Attributes) {
				t.Fatalf("duration = %+v", duration)
			}
			if !tc.scores {
				if len(metrics) != 2 {
					t.Fatalf("unexpected confidence metrics: %+v", metrics)
				}
				return
			}
			probabilities := metrics["ocman.judge.probability"].Data.(metricdata.Histogram[float64]).DataPoints
			if len(probabilities) != 3 {
				t.Fatalf("probabilities = %+v", probabilities)
			}
			wantScores := map[string]float64{"safe": result.scores.Safe, "unsafe": result.scores.Unsafe, "uncertain": result.scores.Uncertain}
			for _, point := range probabilities {
				answer, _ := point.Attributes.Value("answer")
				choice, _ := point.Attributes.Value("choice")
				if point.Count != 1 || point.Sum != wantScores[answer.AsString()] || choice.AsString() != result.scores.Choice || point.Attributes.Len() != 4 {
					t.Errorf("probability = %+v", point)
				}
			}
			threshold := metrics["ocman.judge.safe_threshold"].Data.(metricdata.Histogram[float64]).DataPoints
			if len(threshold) != 1 || threshold[0].Count != 1 || threshold[0].Sum != result.scores.Threshold {
				t.Fatalf("threshold = %+v", threshold)
			}
		})
	}
}
