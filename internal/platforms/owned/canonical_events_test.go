package owned

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/google/uuid"
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
