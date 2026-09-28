package state

import (
	"context"
	"strings"
	"testing"
)

func TestSecretSetting_RoundTripSealed(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, ok, err := db.GetSecretSetting(ctx, "k"); ok || err != nil {
		t.Fatalf("unset = %v %v", ok, err)
	}
	if err := db.SetSecretSetting(ctx, "k", "top-secret"); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := db.GetSetting(ctx, "k")
	if strings.Contains(raw, "top-secret") {
		t.Fatalf("stored in plaintext: %q", raw)
	}
	if v, ok, err := db.GetSecretSetting(ctx, "k"); !ok || err != nil || v != "top-secret" {
		t.Fatalf("got %q %v %v", v, ok, err)
	}
	if err := db.SetSetting(ctx, "k", "!!not base64"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetSecretSetting(ctx, "k"); err == nil {
		t.Fatal("corrupt value accepted")
	}
}
