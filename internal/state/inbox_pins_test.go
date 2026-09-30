package state

import "testing"

func TestPinInboxItemRoundTrip(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.PinInboxItem(t.Context(), "local", "same-id"); err != nil {
		t.Fatal(err)
	}
	first, err := db.PinnedInboxItems(t.Context())
	if err != nil || first[InboxKey{RemoteID: "local", ItemID: "same-id"}] <= 0 {
		t.Fatalf("pinned = %+v, %v", first, err)
	}
	if err := db.PinInboxItem(t.Context(), "local", "same-id"); err != nil {
		t.Fatal(err)
	}
	second, _ := db.PinnedInboxItems(t.Context())
	if second[InboxKey{RemoteID: "local", ItemID: "same-id"}] != first[InboxKey{RemoteID: "local", ItemID: "same-id"}] {
		t.Fatal("re-pinning changed pinned_at")
	}
	if err := db.PinInboxItem(t.Context(), "remote", "same-id"); err != nil {
		t.Fatal(err)
	}
	if err := db.UnpinInboxItem(t.Context(), "local", "same-id"); err != nil {
		t.Fatal(err)
	}
	pinned, _ := db.PinnedInboxItems(t.Context())
	if len(pinned) != 1 || pinned[InboxKey{RemoteID: "remote", ItemID: "same-id"}] <= 0 {
		t.Fatalf("pinned after unpin = %+v", pinned)
	}
}
