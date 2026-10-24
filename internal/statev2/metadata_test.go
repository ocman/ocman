package statev2

import (
	"testing"
)

func TestMetadataPagesAreOwnerScopedAndContainNoConversationPayload(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	if err := d.RegisterInstance(t.Context(), Instance{ID: "foreign", OwnerID: "another-owner", Platform: "opencode", SourceKey: "foreign"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "foreign"} {
		commit(t, d, fixture(id))
	}
	page, err := d.MetadataPage(t.Context(), "owner", Key{}, 1)
	if err != nil || len(page) != 1 || page[0].Instance.ID != "first" || page[0].Session.SessionID != "session" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if len(page[0].Session.Extensions) != 0 {
		t.Fatal("metadata read included opaque payload")
	}
	if page[0].Session.CreatedAt != 10 || page[0].Session.UpdatedAt != 20 {
		t.Fatalf("discovery timestamps lost: %+v", page[0])
	}
	last := page[0].Session.Key
	page, err = d.MetadataPage(t.Context(), "owner", last, 1)
	if err != nil || len(page) != 1 || page[0].Instance.ID != "second" {
		t.Fatalf("second page: %+v %v", page, err)
	}
	one, err := d.Metadata(t.Context(), page[0].Session.Key)
	if err != nil || one.Session.Title != "retained title" || one.Instance.SourceKey != "second" || len(one.Session.Extensions) != 0 {
		t.Fatalf("single metadata: %+v %v", one, err)
	}
	if one.Session.CreatedAt != 10 || one.Session.UpdatedAt != 20 {
		t.Fatalf("point metadata timestamps lost: %+v", one)
	}
	page, err = d.MetadataPage(t.Context(), "owner", page[0].Session.Key, 1)
	if err != nil || len(page) != 0 {
		t.Fatalf("foreign owner leaked: %+v %v", page, err)
	}
	for _, tc := range []struct {
		owner string
		limit int
	}{{"", 1}, {"owner", 0}, {"owner", 501}} {
		if _, err := d.MetadataPage(t.Context(), tc.owner, Key{}, tc.limit); err == nil {
			t.Fatal("invalid metadata page accepted")
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MetadataPage(t.Context(), "owner", Key{}, 1); err == nil {
		t.Fatal("closed store inventory succeeded")
	}
	if _, err := d.Metadata(t.Context(), last); err == nil {
		t.Fatal("closed store metadata succeeded")
	}
}
