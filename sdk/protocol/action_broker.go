package protocol

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

const ActionTimeout = 30 * time.Second

// ActionAuthorization must read current enablement/grants and serialize its
// callback with revocation. The callback only admits a call; it never waits for
// plugin output. Already disclosed context cannot be recalled on revocation.
type ActionAuthorization func(context.Context, string, func(Description, []string) error) error
type ActionCall func(context.Context, string, Call) (<-chan Reply, error)

type actionOperation struct {
	request      ActionRequest
	hash         [32]byte
	confirmation *ActionConfirmation
	done         chan struct{}
	response     ActionResponse
}

type actionArtifact struct {
	operation *actionOperation
	name      string
	data      []byte
}

// ActionBroker retains operation results, including failures, for its entire life.
// It never evicts and re-executes an ID. Capacity exhaustion fails closed. The host
// call adapter also reserves durable operation receipts before dispatch, so retries
// across a host restart return conflict rather than repeat an uncertain effect.
// ponytail: bounded host-lifetime result cache; only artifacts persist (ArtifactRoot).
type ActionBroker struct {
	authorize  ActionAuthorization
	call       ActionCall
	mu         sync.Mutex
	operations map[string]*actionOperation
	artifacts  map[string]actionArtifact
	bytes      int
	timeout    time.Duration
	// ArtifactRoot maps a plugin ID to its private data root. When set, artifacts
	// persist under <root>/artifacts so a handle survives a host restart.
	ArtifactRoot func(pluginID string) (string, error)
	// ResolveDirectory resolves an opaque project ID to its directory on the executing
	// host. It runs at dispatch, after grant authorization, only for project
	// placements declaring context.project.path. An empty result omits the field.
	ResolveDirectory func(ctx context.Context, projectID string) string
}

// artifactMeta is the on-disk record that lets a restarted broker re-run the
// authorization check for a persisted artifact.
type artifactMeta struct {
	Request ActionRequest `json:"request"`
	Hash    [32]byte      `json:"hash"`
	Name    string        `json:"name"`
}

func NewActionBroker(authorize ActionAuthorization, call ActionCall) *ActionBroker {
	return &ActionBroker{authorize: authorize, call: call, operations: map[string]*actionOperation{}, artifacts: map[string]actionArtifact{}, timeout: ActionTimeout}
}

func (e *WireError) Error() string { return string(e.Category) }

func actionFailure(err error) ActionResponse {
	category := ErrorInternal
	var wire *WireError
	switch {
	case errors.As(err, &wire):
		category = wire.Category
	case errors.Is(err, context.DeadlineExceeded):
		category = ErrorDeadlineExceeded
	case errors.Is(err, context.Canceled):
		category = ErrorCancelled
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrBusy):
		category = ErrorUnavailable
	}
	return ActionResponse{Error: &WireError{Category: category}}
}

func authorizedAction(d Description, grants []string, r ActionRequest) (ActionDescriptor, error) {
	for _, a := range d.Actions {
		if a.ID != r.ActionID {
			continue
		}
		if a.Placement != r.Placement || !slices.Contains(a.Surfaces, r.Surface) || !a.Granted(grants) {
			return a, &WireError{Category: ErrorPermissionDenied}
		}
		return a, nil
	}
	return ActionDescriptor{}, &WireError{Category: ErrorNotFound}
}

func actionHash(r ActionRequest, a ActionDescriptor) [32]byte {
	r.ConfirmationToken = ""
	data, _ := json.Marshal(struct {
		Request    ActionRequest
		Descriptor ActionDescriptor
	}{r, a})
	return sha256.Sum256(data)
}

func (b *ActionBroker) Invoke(ctx context.Context, request ActionRequest) ActionResponse {
	if request.Validate() != nil {
		return actionFailure(&WireError{Category: ErrorInvalidArgument})
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return actionFailure(err)
	}
	var op *actionOperation
	var replies <-chan Reply
	var admitted bool
	var confirmation *ActionConfirmation
	err := b.authorize(ctx, request.PluginID, func(d Description, grants []string) error {
		a, err := authorizedAction(d, grants, request)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		key := request.PluginID + "/" + request.OperationID
		hash := actionHash(request, a)
		op = b.operations[key]
		if op != nil && op.hash != hash {
			return &WireError{Category: ErrorConflict}
		}
		if op == nil {
			if len(b.operations) >= 4096 {
				return ErrBusy
			}
			r := request
			r.ConfirmationToken = ""
			r.Context.Selection = append([]ActionSelection(nil), r.Context.Selection...)
			op = &actionOperation{request: r, hash: hash}
			b.operations[key] = op
			if a.Confirmation != "" {
				op.confirmation = &ActionConfirmation{Text: a.Confirmation, Token: rand.Text(), ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli()}
			}
		}
		if op.confirmation != nil && op.done == nil {
			if time.Now().UnixMilli() >= op.confirmation.ExpiresAt {
				return &WireError{Category: ErrorConflict}
			}
			if request.ConfirmationToken != op.confirmation.Token {
				copy := *op.confirmation
				confirmation = &copy
				return nil
			}
		}
		if op.done != nil {
			return nil
		}
		op.done = make(chan struct{})
		admitted = true
		// Resolve the directory here, after authorizedAction proved the grant and
		// after op.request was copied, so neither the operation hash nor persisted
		// artifact metadata ever carries a host path. Minimize still gates it on
		// the required grant and placement.
		request.Context.Directory = ""
		if b.ResolveDirectory != nil && request.Placement == "project" && slices.Contains(a.RequiredGrants, "context.project.path") {
			request.Context.Directory = b.ResolveDirectory(ctx, request.Context.ProjectID)
		}
		params, _ := json.Marshal(ActionInvocation{ActionID: a.ID, Context: a.Minimize(request.Context)})
		deadline, _ := ctx.Deadline()
		replies, err = b.call(ctx, request.PluginID, Call{OperationID: request.OperationID, Capability: "action", Version: ActionCapability.Version, Method: "invoke", DeadlineUnixMS: deadline.UnixMilli(), Params: params})
		if err != nil {
			op.response = actionFailure(err)
			close(op.done)
			admitted = false
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return actionFailure(ctx.Err())
		}
		return actionFailure(err)
	}
	if confirmation != nil {
		return ActionResponse{Confirmation: confirmation}
	}
	if admitted {
		response := b.receive(ctx, replies)
		b.mu.Lock()
		op.response = b.storeArtifacts(op, response)
		close(op.done)
		b.mu.Unlock()
	}
	select {
	case <-op.done:
		if ctx.Err() != nil {
			return actionFailure(ctx.Err())
		}
		// Re-check even cached results. Revoked grants never authorize another
		// invocation, result delivery, or artifact download.
		if err := b.check(ctx, op); err != nil {
			if ctx.Err() != nil {
				return actionFailure(ctx.Err())
			}
			return actionFailure(err)
		}
		data, _ := json.Marshal(op.response)
		var response ActionResponse
		_ = json.Unmarshal(data, &response)
		return response
	case <-ctx.Done():
		return actionFailure(ctx.Err())
	}
}

func (b *ActionBroker) check(ctx context.Context, op *actionOperation) error {
	return b.authorize(ctx, op.request.PluginID, func(d Description, grants []string) error {
		a, err := authorizedAction(d, grants, op.request)
		if err != nil {
			return err
		}
		if actionHash(op.request, a) != op.hash {
			return &WireError{Category: ErrorConflict}
		}
		return nil
	})
}

func (b *ActionBroker) receive(ctx context.Context, replies <-chan Reply) ActionResponse {
	select {
	case <-ctx.Done():
		return actionFailure(ctx.Err())
	case reply, ok := <-replies:
		if !ok {
			return actionFailure(ErrUnavailable)
		}
		if reply.Err != nil {
			return actionFailure(reply.Err)
		}
		r := reply.Message.Result
		// action.v1 is unary, even though the transport supports streaming.
		if reply.Message.Type != TypeResult || r == nil {
			return actionFailure(ErrInvalidMessage)
		}
		if r.Error != nil {
			return actionFailure(r.Error)
		}
		results, err := DecodeActionResults(r.Value)
		if err != nil {
			return actionFailure(err)
		}
		return ActionResponse{Results: results}
	}
}

// Caller holds mu. Artifact bytes never enter a browser invocation response.
func (b *ActionBroker) storeArtifacts(op *actionOperation, response ActionResponse) ActionResponse {
	size := 0
	for _, r := range response.Results {
		size += len(r.Data) + len(r.Text) + len(r.URL) + len(r.Label)
	}
	if b.bytes+size > 32<<20 {
		return actionFailure(ErrBusy)
	}
	b.bytes += size
	for i := range response.Results {
		r := &response.Results[i]
		if r.Kind != "artifact" {
			continue
		}
		id := rand.Text()
		r.Handle = base64.RawURLEncoding.EncodeToString([]byte(op.request.PluginID)) + "." + id
		if err := b.persistArtifact(op, id, r.Label, r.Data); err != nil {
			return actionFailure(ErrUnavailable)
		}
		// Persisted artifacts are always read back from disk, so permanent
		// removal of the data root revokes the handle in this broker too.
		if b.ArtifactRoot == nil {
			b.artifacts[r.Handle] = actionArtifact{operation: op, name: r.Label, data: r.Data}
		}
		r.Data = nil
	}
	return response
}

func (b *ActionBroker) artifactDir(pluginID string) (*os.Root, error) {
	dir, err := b.ArtifactRoot(pluginID)
	if err != nil {
		return nil, err
	}
	// os.Root keeps plugin-planted symlinks from escaping the data root.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := root.Mkdir("artifacts", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		_ = root.Close()
		return nil, err
	}
	sub, err := root.OpenRoot("artifacts")
	_ = root.Close()
	return sub, err
}

func (b *ActionBroker) persistArtifact(op *actionOperation, id, name string, data []byte) error {
	if b.ArtifactRoot == nil {
		return nil
	}
	root, err := b.artifactDir(op.request.PluginID)
	if err != nil {
		return err
	}
	defer root.Close()
	meta, _ := json.Marshal(artifactMeta{Request: op.request, Hash: op.hash, Name: name})
	if err := root.WriteFile(id+"-"+name, data, 0o600); err != nil {
		return err
	}
	return root.WriteFile(id+".json", meta, 0o600)
}

// loadArtifact recovers an artifact persisted by an earlier broker instance.
func (b *ActionBroker) loadArtifact(handle string) (actionArtifact, bool) {
	encoded, id, ok := strings.Cut(handle, ".")
	plugin, err := base64.RawURLEncoding.DecodeString(encoded)
	if !ok || err != nil || b.ArtifactRoot == nil || id == "" || strings.Trim(id, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
		return actionArtifact{}, false
	}
	root, err := b.artifactDir(string(plugin))
	if err != nil {
		return actionArtifact{}, false
	}
	defer root.Close()
	raw, err := root.ReadFile(id + ".json")
	var meta artifactMeta
	if err != nil || json.Unmarshal(raw, &meta) != nil || meta.Request.PluginID != string(plugin) || meta.Name == "" || strings.ContainsAny(meta.Name, `/\`) {
		return actionArtifact{}, false
	}
	data, err := root.ReadFile(id + "-" + meta.Name)
	if err != nil {
		return actionArtifact{}, false
	}
	return actionArtifact{operation: &actionOperation{request: meta.Request, hash: meta.Hash}, name: meta.Name, data: data}, true
}

func (b *ActionBroker) Artifact(ctx context.Context, handle string) (string, []byte, error) {
	b.mu.Lock()
	a, ok := b.artifacts[handle]
	b.mu.Unlock()
	if !ok {
		a, ok = b.loadArtifact(handle)
	}
	if !ok {
		return "", nil, &WireError{Category: ErrorNotFound}
	}
	if err := b.check(ctx, a.operation); err != nil {
		return "", nil, err
	}
	return a.name, append([]byte(nil), a.data...), nil
}
