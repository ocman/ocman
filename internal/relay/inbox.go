package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"filippo.io/age"
	"github.com/NoUseFreak/ocman/internal/share"
)

const inboxFormatVersion = 1

const inboxPageSize = 50

type inboxMeta struct {
	Version            int            `json:"v"`
	Recipient          string         `json:"recipient"`
	KeyVersion         int            `json:"keyVersion"`
	IngestHash         string         `json:"ingestHash"`
	ManagementHash     string         `json:"managementHash"`
	FetchHash          string         `json:"fetchHash"`
	AcknowledgmentHash string         `json:"acknowledgmentHash"`
	CreatedAt          int64          `json:"createdAt"`
	SecretHash         string         `json:"secretHash,omitempty"`
	SecretHeader       string         `json:"secretHeader,omitempty"`
	Recipients         map[int]string `json:"recipients,omitempty"`
}

type inboxRegistrationRequest struct {
	Recipient    string `json:"recipient"`
	Secret       string `json:"secret,omitempty"`
	SecretHeader string `json:"secretHeader,omitempty"`
}

type inboxRegistrationResponse struct {
	ID                  string `json:"id"`
	IngestionURL        string `json:"ingestionUrl"`
	ManagementToken     string `json:"managementToken"`
	FetchToken          string `json:"fetchToken"`
	AcknowledgmentToken string `json:"acknowledgmentToken"`
	KeyVersion          int    `json:"keyVersion"`
	SecretHeader        string `json:"secretHeader,omitempty"`
}

type inboxRotationRequest struct {
	Recipient string `json:"recipient"`
}

type inboxIngestResponse struct {
	DeliveryID string `json:"deliveryId"`
}

type inboxDelivery struct {
	ID string `json:"id"`
}

type inboxDeliveryPage struct {
	Deliveries []inboxDelivery `json:"deliveries"`
	Cursor     string          `json:"cursor"`
}

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

func (s *Server) handleRegisterInbox(w http.ResponseWriter, r *http.Request) {
	if !tokenAuthorises(hashToken(s.cfg.EnrollmentToken), bearerToken(r)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request inboxRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid registration", http.StatusBadRequest)
		return
	}
	recipient, err := age.ParseX25519Recipient(request.Recipient)
	if err != nil {
		http.Error(w, "invalid X25519 recipient", http.StatusBadRequest)
		return
	}
	id, err := randomString(16)
	if err != nil {
		serverError(w, err)
		return
	}
	ingestToken, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	managementToken, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	fetchToken, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	acknowledgmentToken, err := randomString(32)
	if err != nil {
		serverError(w, err)
		return
	}
	m := inboxMeta{
		Version:            inboxFormatVersion,
		Recipient:          recipient.String(),
		KeyVersion:         1,
		IngestHash:         hashToken(ingestToken),
		ManagementHash:     hashToken(managementToken),
		FetchHash:          hashToken(fetchToken),
		AcknowledgmentHash: hashToken(acknowledgmentToken),
		CreatedAt:          s.cfg.Now().UnixMilli(),
		SecretHeader:       request.SecretHeader,
	}
	if request.Secret != "" {
		m.SecretHash = hashToken(request.Secret)
		if m.SecretHeader == "" {
			m.SecretHeader = s.cfg.InboxSecretHeader
		}
	}
	m.Recipients = map[int]string{1: m.Recipient}
	if err := putInboxMeta(r.Context(), s.cfg.Store, id, m); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, inboxRegistrationResponse{
		ID:                  id,
		IngestionURL:        "/i/" + id + "/" + ingestToken,
		ManagementToken:     managementToken,
		FetchToken:          fetchToken,
		AcknowledgmentToken: acknowledgmentToken,
		KeyVersion:          1,
		SecretHeader:        m.SecretHeader,
	})
}

func (s *Server) handleManageInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, ok := s.authoriseInbox(w, r, id, func(m inboxMeta) string { return m.ManagementHash })
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID         string `json:"id"`
		Recipient  string `json:"recipient"`
		KeyVersion int    `json:"keyVersion"`
	}{id, m.Recipient, m.KeyVersion})
}

func (s *Server) handleIngestInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, found, err := getInboxMeta(r.Context(), s.cfg.Store, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if !found || !tokenAuthorises(m.IngestHash, r.PathValue("token")) {
		http.Error(w, "inbox not found", http.StatusNotFound)
		return
	}
	if m.SecretHash != "" {
		secret := r.Header.Get(m.SecretHeader)
		if subtle.ConstantTimeCompare([]byte(hashToken(secret)), []byte(m.SecretHash)) != 1 {
			http.Error(w, "invalid webhook secret", http.StatusUnauthorized)
			return
		}
	}
	if !s.inboxIngest.allow(id) {
		http.Error(w, "too many webhook deliveries", http.StatusTooManyRequests)
		return
	}
	recipient, err := age.ParseX25519Recipient(m.currentRecipient())
	if err != nil {
		serverError(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxInboxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "webhook body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid body", http.StatusBadRequest)
		}
		return
	}
	deliveryID, err := randomString(16)
	if err != nil {
		serverError(w, err)
		return
	}
	envelope := InboxEnvelope{
		FormatVersion: inboxFormatVersion,
		KeyVersion:    m.KeyVersion,
		InboxID:       id,
		DeliveryID:    deliveryID,
		Body:          body,
		Request: InboxRequest{
			Method:     r.Method,
			Header:     nil,
			Query:      r.URL.Query(),
			ReceivedAt: s.cfg.Now().UnixMilli(),
		},
	}
	var headersOK bool
	envelope.Request.Header, headersOK = retainedHeaders(r.Header, m.SecretHeader, s.cfg.MaxInboxHeaderBytes)
	if !headersOK {
		http.Error(w, "webhook headers too large", http.StatusRequestEntityTooLarge)
		return
	}
	ciphertext, err := encryptInboxEnvelope(recipient, envelope)
	if err != nil {
		serverError(w, err)
		return
	}
	s.mutations.Lock()
	defer s.mutations.Unlock()
	objects, err := s.cfg.Store.List(r.Context(), inboxDeliveryPrefix(id))
	if err != nil {
		serverError(w, err)
		return
	}
	if len(objects) >= s.cfg.MaxInboxPendingDeliveries {
		http.Error(w, "inbox has too many pending deliveries", http.StatusRequestEntityTooLarge)
		return
	}
	var pending int64
	for _, object := range objects {
		pending += object.Size
	}
	if pending+int64(len(ciphertext)) > s.cfg.MaxInboxPendingBytes {
		http.Error(w, "inbox pending quota exceeded", http.StatusRequestEntityTooLarge)
		return
	}
	if err := s.cfg.Store.Put(r.Context(), inboxDeliveryKey(id, deliveryID), ciphertext); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, inboxIngestResponse{DeliveryID: deliveryID})
}

func (s *Server) handleRotateInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, ok := s.authoriseInbox(w, r, id, func(m inboxMeta) string { return m.ManagementHash })
	if !ok {
		return
	}
	var request inboxRotationRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request) != nil {
		http.Error(w, "invalid recipient", http.StatusBadRequest)
		return
	}
	recipient, err := age.ParseX25519Recipient(request.Recipient)
	if err != nil {
		http.Error(w, "invalid X25519 recipient", http.StatusBadRequest)
		return
	}
	s.mutations.Lock()
	defer s.mutations.Unlock()
	m.KeyVersion++
	if m.Recipients == nil {
		m.Recipients = map[int]string{1: m.Recipient}
	}
	m.Recipients[m.KeyVersion] = recipient.String()
	m.Recipient = recipient.String()
	if err := putInboxMeta(r.Context(), s.cfg.Store, id, m); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		KeyVersion int `json:"keyVersion"`
	}{m.KeyVersion})
}

func (s *Server) handleListInboxDeliveries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.authoriseInbox(w, r, id, func(m inboxMeta) string { return m.FetchHash }); !ok {
		return
	}
	objects, err := s.cfg.Store.List(r.Context(), inboxDeliveryPrefix(id))
	if err != nil {
		serverError(w, err)
		return
	}
	cursor := r.URL.Query().Get("cursor")
	after, through := "", ""
	if cursor != "" {
		var ok bool
		after, through, ok = decodeInboxCursor(cursor)
		if !ok {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
	}
	ids := make([]string, 0, len(objects))
	for _, object := range objects {
		id, ok := deliveryIDFromKey(object.Key)
		if ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if cursor == "" && len(ids) > 0 {
		through = ids[len(ids)-1]
	}
	ids = filterDeliveryPage(ids, after, through)
	if len(ids) > inboxPageSize {
		ids = ids[:inboxPageSize]
	}
	page := inboxDeliveryPage{Deliveries: make([]inboxDelivery, len(ids))}
	for i, id := range ids {
		page.Deliveries[i] = inboxDelivery{ID: id}
	}
	if len(ids) == inboxPageSize {
		page.Cursor = encodeInboxCursor(ids[len(ids)-1], through)
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleFetchInboxDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.authoriseInbox(w, r, id, func(m inboxMeta) string { return m.FetchHash }); !ok {
		return
	}
	deliveryID := r.PathValue("deliveryID")
	if !validDeliveryID(deliveryID) {
		http.Error(w, "delivery not found", http.StatusNotFound)
		return
	}
	ciphertext, err := s.cfg.Store.Get(r.Context(), inboxDeliveryKey(id, deliveryID))
	if errors.Is(err, share.ErrNotFound) {
		http.Error(w, "delivery not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(ciphertext)
}

func (s *Server) handleAcknowledgeInboxDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.authoriseInbox(w, r, id, func(m inboxMeta) string { return m.AcknowledgmentHash }); !ok {
		return
	}
	deliveryID := r.PathValue("deliveryID")
	if !validDeliveryID(deliveryID) {
		http.Error(w, "delivery not found", http.StatusNotFound)
		return
	}
	if err := s.cfg.Store.DeletePrefix(r.Context(), inboxDeliveryKey(id, deliveryID)); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevokeInbox(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authoriseInbox(w, r, r.PathValue("id"), func(m inboxMeta) string { return m.ManagementHash }); !ok {
		return
	}
	s.mutations.Lock()
	defer s.mutations.Unlock()
	if err := s.cfg.Store.DeletePrefix(r.Context(), "inboxes/"+r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if envelope.FormatVersion != inboxFormatVersion || envelope.KeyVersion < 1 {
		return InboxEnvelope{}, fmt.Errorf("unsupported inbox envelope version")
	}
	if envelope.InboxID != inboxID || envelope.DeliveryID != deliveryID {
		return InboxEnvelope{}, fmt.Errorf("inbox delivery identity mismatch")
	}
	return envelope, nil
}

func encryptInboxEnvelope(recipient age.Recipient, envelope InboxEnvelope) ([]byte, error) {
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

func randomString(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("relay: generating inbox credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validRandomString(value string, bytes int) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(bytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == bytes
}

func validDeliveryID(value string) bool {
	return validRandomString(value, 16)
}

func filterDeliveryPage(ids []string, after, through string) []string {
	page := ids[:0]
	for _, id := range ids {
		if id > after && id <= through {
			page = append(page, id)
		}
	}
	return page
}

func encodeInboxCursor(after, through string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(after + "\n" + through))
}

func decodeInboxCursor(cursor string) (after, through string, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false
	}
	after, through, found := strings.Cut(string(decoded), "\n")
	return after, through, found && validDeliveryID(after) && validDeliveryID(through) && after <= through
}

func tokenAuthorises(hash, token string) bool {
	return token != "" && hash != "" && meta{DeleteHash: hash}.authorises(token)
}

func putInboxMeta(ctx context.Context, store share.Store, id string, m inboxMeta) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return store.Put(ctx, inboxMetaKey(id), data)
}

func getInboxMeta(ctx context.Context, store share.Store, id string) (inboxMeta, bool, error) {
	if !validRandomString(id, 16) {
		return inboxMeta{}, false, nil
	}
	data, err := store.Get(ctx, inboxMetaKey(id))
	if errors.Is(err, share.ErrNotFound) {
		return inboxMeta{}, false, nil
	}
	if err != nil {
		return inboxMeta{}, false, err
	}
	var m inboxMeta
	if err := json.Unmarshal(data, &m); err != nil || m.Version != inboxFormatVersion {
		return inboxMeta{}, false, fmt.Errorf("invalid inbox metadata")
	}
	if m.Recipients == nil && m.Recipient != "" {
		m.Recipients = map[int]string{m.KeyVersion: m.Recipient}
	}
	return m, true, nil
}

func (m inboxMeta) currentRecipient() string { return m.Recipients[m.KeyVersion] }

var sensitiveInboxHeaders = map[string]bool{
	"authorization": true, "cookie": true, "set-cookie": true,
	"proxy-authorization": true, "x-webhook-secret": true,
}

func retainedHeaders(src http.Header, secretHeader string, max int64) (http.Header, bool) {
	out := make(http.Header)
	var size int64
	for name, values := range src {
		if strings.EqualFold(name, secretHeader) || sensitiveInboxHeaders[strings.ToLower(name)] {
			continue
		}
		for _, value := range values {
			candidate := int64(len(name) + len(value) + 4)
			if size+candidate > max {
				return nil, false
			}
			out.Add(name, value)
			size += candidate
		}
	}
	return out, true
}

func (s *Server) authoriseInbox(w http.ResponseWriter, r *http.Request, id string, credential func(inboxMeta) string) (inboxMeta, bool) {
	m, found, err := getInboxMeta(r.Context(), s.cfg.Store, id)
	if err != nil {
		serverError(w, err)
		return inboxMeta{}, false
	}
	if !found || !tokenAuthorises(credential(m), bearerToken(r)) {
		http.Error(w, "inbox not found", http.StatusNotFound)
		return inboxMeta{}, false
	}
	return m, true
}

func inboxMetaKey(id string) string {
	return "inboxes/" + id + "/meta"
}

func inboxDeliveryPrefix(id string) string {
	return "inboxes/" + id + "/deliveries/"
}

func inboxDeliveryKey(id, deliveryID string) string {
	return inboxDeliveryPrefix(id) + deliveryID
}

func deliveryIDFromKey(key string) (string, bool) {
	id := key[strings.LastIndexByte(key, '/')+1:]
	return id, validDeliveryID(id)
}
