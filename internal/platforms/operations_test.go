package platforms

import (
	"encoding/json"
	"testing"
)

func TestSessionModelAvailabilityJSON(t *testing.T) {
	for _, available := range []bool{false, true} {
		body, err := json.Marshal(SessionModel{IsAvailable: available})
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			IsAvailable *bool `json:"isAvailable"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.IsAvailable == nil || *decoded.IsAvailable != available {
			t.Fatalf("availability %v must be explicit in JSON, got %s", available, body)
		}
	}
}
