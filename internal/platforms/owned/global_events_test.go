package owned

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type globalEventPlatform struct{ *nativePlatform }

func (p *globalEventPlatform) ProxyEvents(_ context.Context, id string, out io.Writer, flush func()) error {
	if id != "" {
		return io.ErrUnexpectedEOF
	}
	_, err := io.WriteString(out, p.events)
	if flush != nil {
		flush()
	}
	return err
}

func TestOwnerGlobalStreamProjectsNamedAndDirectoryWrappedEvents(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	a.native = &globalEventPlatform{nativePlatform: p}
	p.events = "event: permission.v2.asked\ndata: {\"id\":\"native-permission\",\"sessionID\":\"native\",\"metadata\":{\"sessionID\":\"opaque-native\"}}\n\n" +
		"data: {\"directory\":\"/repo\",\"payload\":{\"type\":\"ocman.queue.changed\",\"properties\":{\"sessionID\":\"native\"}}}\n\n"
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	var output bytes.Buffer
	if err := a.ProxyEvents(t.Context(), "", &output, nil); err != nil {
		t.Fatal(err)
	}
	var values []map[string]any
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			var value map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
	}
	if len(values) != 2 || values[0]["sessionID"] != id || values[0]["id"] != "native-permission" || values[0]["metadata"].(map[string]any)["sessionID"] != "opaque-native" {
		t.Fatalf("named global event lost identity or opaque payload: %s", output.String())
	}
	if values[1]["payload"].(map[string]any)["properties"].(map[string]any)["sessionID"] != id {
		t.Fatalf("directory-wrapped event exposed native reference: %s", output.String())
	}
}
