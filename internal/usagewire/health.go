package usagewire

// Health contains only daily counters and non-cumulative latency buckets.
type Health struct {
	Platform string      `json:"platform"`
	Starts   StartCounts `json:"starts"`
	Turns    TurnCounts  `json:"turns"`
}

type StartCounts struct {
	Ready    int64 `json:"ready,omitempty"`
	Failed   int64 `json:"failed,omitempty"`
	Timeout  int64 `json:"timeout,omitempty"`
	Canceled int64 `json:"canceled,omitempty"`
}

type TurnCounts struct {
	Started       int64   `json:"started,omitempty"`
	Completed     int64   `json:"completed,omitempty"`
	Failed        int64   `json:"failed,omitempty"`
	Interrupted   int64   `json:"interrupted,omitempty"`
	Canceled      int64   `json:"canceled,omitempty"`
	NoResponse    int64   `json:"noResponse,omitempty"`
	FirstResponse []int64 `json:"firstResponse,omitempty"`
}

// ResponseBucketCount covers <1s, <2s, <5s, <10s, <30s, <60s, and >=60s.
const ResponseBucketCount = 7

func HealthPlatform(s string) bool {
	switch s {
	case "opencode", "claude", "codex":
		return true
	}
	return false
}

func validHealth(rows []Health) bool {
	if len(rows) == 0 || len(rows) > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if !HealthPlatform(row.Platform) || seen[row.Platform] {
			return false
		}
		seen[row.Platform] = true
		s, t := row.Starts, row.Turns
		if len(t.FirstResponse) != 0 && len(t.FirstResponse) != ResponseBucketCount {
			return false
		}
		counts := []int64{s.Ready, s.Failed, s.Timeout, s.Canceled, t.Started, t.Completed, t.Failed, t.Interrupted, t.Canceled, t.NoResponse}
		var total int64
		for _, count := range append(counts, t.FirstResponse...) {
			if count < 0 || count > 1_000_000 {
				return false
			}
			total += count
		}
		if total == 0 {
			return false
		}
	}
	return true
}
