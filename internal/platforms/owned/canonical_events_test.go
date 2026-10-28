package owned

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms/identity"
)

func TestCanonicalEventValidatorRejectsNativeAndPendingWithoutClearingOrAllocating(t *testing.T) {
	for _, id := range []string{"native", "ocm_invalid", identity.PendingReference()} {
		var out bytes.Buffer
		writer := CanonicalEventValidator(t.Context(), &out)
		if _, err := io.WriteString(writer, fmt.Sprintf("event: permission.v2.asked\ndata: {\"sessionID\":%q,\"id\":\"native-request\"}\n\n", id)); err == nil || out.Len() != 0 {
			t.Fatalf("unproven peer frame was forwarded: %q %v", out.String(), err)
		}
	}
	id := "ocm_" + uuid.NewString()
	var out bytes.Buffer
	writer := CanonicalEventValidator(t.Context(), &out)
	frame := fmt.Sprintf("data: {\"directory\":\"/repo\",\"payload\":{\"type\":\"permission.asked\",\"properties\":{\"sessionID\":%q,\"id\":\"native-request\",\"metadata\":{\"sessionID\":\"opaque-native\"}}}}\n\n", id)
	for _, fragment := range []string{frame[:20], frame[20:]} {
		if _, err := io.WriteString(writer, fragment); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil || !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "opaque-native") || !strings.Contains(out.String(), "native-request") {
		t.Fatalf("canonical event lost correlation or opaque data: %q %v", out.String(), err)
	}
	if _, err := io.WriteString(writer, "data: {"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err == nil {
		t.Fatal("partial peer frame accepted")
	}
}

func TestCanonicalEventValidatorChecksHistoryCommitSessions(t *testing.T) {
	frame := func(id string) string {
		return fmt.Sprintf("event: ocman.history.committed\ndata: {\"type\":\"ocman.history.committed\",\"properties\":{\"sessionID\":%q,\"revision\":3}}\n\n", id)
	}
	var out bytes.Buffer
	if _, err := io.WriteString(CanonicalEventValidator(t.Context(), &out), frame("native")); err == nil || out.Len() != 0 {
		t.Fatalf("native commit session forwarded: %q %v", out.String(), err)
	}
	id := "ocm_" + uuid.NewString()
	if _, err := io.WriteString(CanonicalEventValidator(t.Context(), &out), frame(id)); err != nil || !strings.Contains(out.String(), id) {
		t.Fatalf("canonical commit frame dropped: %q %v", out.String(), err)
	}
}
