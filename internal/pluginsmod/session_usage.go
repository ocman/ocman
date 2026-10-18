package pluginsmod

import (
	"database/sql"
	"encoding/json"
	"math"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

func migratePluginSessionUsage(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE plugin_session_outbox ADD COLUMN usage_json TEXT`)
	return err
}

// completedUsage includes every assistant message in the completed user turn,
// including reasoning/tool-loop messages. Missing metadata remains unknown.
func completedUsage(messages []db.Message, id string) *plugins.SessionUsage {
	type messageData struct {
		db.MessageData
		ParentID string   `json:"parentID"`
		Cost     *float64 `json:"cost"`
	}
	var selected messageData
	found := false
	for _, message := range messages {
		if message.ID == id {
			found = json.Unmarshal(message.Data, &selected) == nil
			break
		}
	}
	if !found || selected.Role != "assistant" || selected.Tokens == nil || selected.Cost == nil || selected.UsageAvailable != nil && !*selected.UsageAvailable {
		return nil
	}
	u := new(plugins.SessionUsage)
	for _, message := range messages {
		var data messageData
		if json.Unmarshal(message.Data, &data) != nil {
			return nil
		}
		if message.ID != id && (selected.ParentID == "" || data.ParentID != selected.ParentID || data.Role != "assistant") {
			continue
		}
		if data.Tokens == nil || data.Cost == nil || data.UsageAvailable != nil && !*data.UsageAvailable {
			return nil
		}
		prompt, completion := []int64{data.Tokens.Input}, []int64{data.Tokens.Output, data.Tokens.Reasoning}
		if data.Tokens.Cache != nil {
			prompt = append(prompt, data.Tokens.Cache.Read, data.Tokens.Cache.Write)
		}
		p, pok := sumUsage(prompt...)
		c, cok := sumUsage(completion...)
		if !pok || !cok {
			return nil
		}
		p, pok = sumUsage(u.PromptTokens, p)
		c, cok = sumUsage(u.CompletionTokens, c)
		if !pok || !cok {
			return nil
		}
		u.PromptTokens, u.CompletionTokens, u.CostUSD = p, c, u.CostUSD+*data.Cost
		if u.Validate() != nil || *data.Cost < 0 {
			return nil
		}
	}
	return u
}

func sumUsage(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, false
		}
		total += value
	}
	return total, true
}
