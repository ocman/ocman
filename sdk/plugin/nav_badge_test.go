package plugin

import (
	"testing"

	"github.com/ocman/ocman/sdk/protocol"
)

func TestNewNavBadge(t *testing.T) {
	for _, badge := range []NavBadge{{Kind: NavBadgeDot}, {Kind: NavBadgeCount, Count: 1}, {Kind: NavBadgeCount, Count: NavBadgeMaxCount}, {Kind: NavBadgeClear}} {
		event, err := NewNavBadge(badge)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := protocol.DecodeNavBadge(event); err != nil || got != badge {
			t.Fatalf("round trip = %+v, %v; want %+v", got, err, badge)
		}
	}
	for _, badge := range []NavBadge{{}, {Kind: NavBadgeCount}, {Kind: NavBadgeCount, Count: -1}, {Kind: NavBadgeCount, Count: NavBadgeMaxCount + 1}, {Kind: NavBadgeDot, Count: 1}, {Kind: NavBadgeClear, Count: 1}} {
		if _, err := NewNavBadge(badge); err == nil {
			t.Errorf("accepted %+v", badge)
		}
	}
}
