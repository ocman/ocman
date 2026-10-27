package codexprovider

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// recordedResult returns the result of the first recorded call of method.
func recordedResult(t *testing.T, dir, method string, out any) {
	t.Helper()
	f, err := os.Open(dir + "transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sent := map[string]string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 16<<20)
	for scanner.Scan() {
		var line struct {
			Dir string `json:"dir"`
			Msg wire   `json:"msg"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &line)
		if line.Dir == "send" && line.Msg.Method != "" {
			sent[string(line.Msg.ID)] = line.Msg.Method
		} else if line.Dir == "recv" && line.Msg.Method == "" && sent[string(line.Msg.ID)] == method {
			if err := json.Unmarshal(line.Msg.Result, out); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no recorded %s", method)
}

func TestAccount(t *testing.T) {
	var account codexappserver.AccountResponse
	var limits codexappserver.RateLimitsResponse
	recordedResult(t, extras, codexappserver.MethodAccountRead, &account)
	recordedResult(t, extras, codexappserver.MethodRateLimits, &limits)
	got, err := Account(&account, &limits.RateLimits)
	if err != nil {
		t.Fatal(err)
	}
	// The recording: a pro plan with only a weekly window, 3% used.
	if got.SubscriptionType != "pro" || len(got.RateLimits) != 1 || got.RateLimits[0].Name != "weekly" ||
		got.RateLimits[0].Utilization != 0.03 || got.RateLimits[0].ResetsAt != 1793602742000 {
		t.Fatalf("account = %+v", got)
	}
	if _, err := Account(&codexappserver.AccountResponse{}, nil); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("signed out = %v", err)
	}
	five, odd := int64(300), int64(60)
	for typ, want := range map[string]string{"apiKey": "API key", "amazonBedrock": "Amazon Bedrock", "future": "future"} {
		a := &codexappserver.AccountResponse{}
		_ = json.Unmarshal([]byte(`{"account":{"type":"`+typ+`"}}`), a)
		got, err := Account(a, &codexappserver.RateLimitSnapshot{Primary: &codexappserver.RateLimitWindow{UsedPercent: 50, WindowDurationMins: &five},
			Secondary: &codexappserver.RateLimitWindow{WindowDurationMins: &odd}})
		if err != nil || got.SubscriptionType != want || got.RateLimits[0].Name != "five_hour" || got.RateLimits[1].Name != "60_minutes" || got.RateLimits[1].ResetsAt != 0 {
			t.Errorf("%s = %+v %v", typ, got, err)
		}
	}
	if got, _ := Account(&account, nil); len(got.RateLimits) != 0 {
		t.Errorf("without limits = %+v", got)
	}
	if windowName(nil) != "usage" {
		t.Error("unnamed window")
	}
}
