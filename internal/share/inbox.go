package share

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"filippo.io/age"
)

// InboxEnvelopeVersion is the only envelope format DecryptInboxEnvelope
// accepts.
const InboxEnvelopeVersion = 1

// InboxEnvelope is the authenticated plaintext inside an age file.
type InboxEnvelope struct {
	FormatVersion int          `json:"formatVersion"`
	KeyVersion    int          `json:"keyVersion"`
	InboxID       string       `json:"inboxId"`
	DeliveryID    string       `json:"deliveryId"`
	Body          []byte       `json:"body"`
	Request       InboxRequest `json:"request"`
}

// InboxRequest is the request metadata retained with an inbox delivery.
type InboxRequest struct {
	Method     string      `json:"method"`
	Header     http.Header `json:"header"`
	Query      url.Values  `json:"query"`
	ReceivedAt int64       `json:"receivedAt"`
}

// DecryptInboxEnvelope decrypts and validates one delivery for its expected
// inbox and delivery identity.
func DecryptInboxEnvelope(identity age.Identity, inboxID, deliveryID string, ciphertext []byte) (InboxEnvelope, error) {
	reader, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return InboxEnvelope{}, fmt.Errorf("decrypting inbox delivery: %w", err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		return InboxEnvelope{}, fmt.Errorf("authenticating inbox delivery: %w", err)
	}
	var envelope InboxEnvelope
	if err := json.Unmarshal(plain, &envelope); err != nil {
		return InboxEnvelope{}, fmt.Errorf("decoding inbox delivery: %w", err)
	}
	if envelope.FormatVersion != InboxEnvelopeVersion || envelope.KeyVersion < 1 {
		return InboxEnvelope{}, fmt.Errorf("unsupported inbox envelope version")
	}
	if envelope.InboxID != inboxID || envelope.DeliveryID != deliveryID {
		return InboxEnvelope{}, fmt.Errorf("inbox delivery identity mismatch")
	}
	return envelope, nil
}

// EncryptInboxEnvelope seals envelope for recipient as an age file.
func EncryptInboxEnvelope(recipient age.Recipient, envelope InboxEnvelope) ([]byte, error) {
	plain, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encoding inbox delivery: %w", err)
	}
	var ciphertext bytes.Buffer
	writer, err := age.Encrypt(&ciphertext, recipient)
	if err != nil {
		return nil, fmt.Errorf("encrypting inbox delivery: %w", err)
	}
	if _, err := writer.Write(plain); err != nil {
		return nil, fmt.Errorf("encrypting inbox delivery: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("encrypting inbox delivery: %w", err)
	}
	return ciphertext.Bytes(), nil
}
