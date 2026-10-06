package state

import (
	"errors"
	"testing"
)

func TestPluginConversationReplyReceipt(t *testing.T) {
	d := outboxDB(t)
	key := conversationKey("account", "thread")
	if has, err := d.HasPluginConversationReply(t.Context(), key, "reply"); has || err != nil {
		t.Fatalf("missing = %v, %v", has, err)
	}
	id := appendReply(t, d, key, "reply", "answer")
	for _, status := range []string{"pending", "dead", "done"} {
		if _, err := d.db.Exec(`UPDATE plugin_conversation_outbox SET status = ? WHERE id = ?`, status, id); err != nil {
			t.Fatal(err)
		}
		if has, err := d.HasPluginConversationReply(t.Context(), key, "reply"); !has || err != nil {
			t.Fatalf("%s receipt = %v, %v", status, has, err)
		}
	}
	other := key
	other.PluginID = "other"
	if has, err := d.HasPluginConversationReply(t.Context(), other, "reply"); has || err != nil {
		t.Fatalf("other plugin = %v, %v", has, err)
	}
	if _, err := d.HasPluginConversationReply(t.Context(), PluginConversationKey{}, "reply"); !errors.Is(err, ErrPluginInvalid) {
		t.Fatal(err)
	}
	if _, err := d.HasPluginConversationReply(t.Context(), key, ""); !errors.Is(err, ErrPluginInvalid) {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.HasPluginConversationReply(t.Context(), key, "reply"); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
}
