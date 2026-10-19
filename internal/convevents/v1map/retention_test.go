package v1map

import (
	"fmt"
	"strings"
	"testing"
)

func TestMapperAnchorsRetainLengthsNotCompletedConversation(t *testing.T) {
	m := newTestMapper(Options{})
	for i := range 10000 {
		m.Map(frame(fmt.Sprintf(`{"type":"message.part.updated","properties":{"part":{"sessionID":"s","messageID":"m%d","id":"p%d","type":"text","text":%q,"time":{"end":1}}}}`, i, i, strings.Repeat("text", 100))))
	}
	if len(m.parts) > 4096 {
		t.Fatalf("retained %d completed anchors", len(m.parts))
	}
	for _, part := range m.parts {
		if size := len(fmt.Sprint(part.fields)); size > 100 {
			t.Fatalf("retained full field text (%d bytes)", size)
		}
	}
}
