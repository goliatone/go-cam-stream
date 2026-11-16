package viewer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

const managedSchemaVersion = 1
const maxManagedStateBytes = 4 * 1024 * 1024
const managedStateName = "managed-state.json"
const managedCandidateName = ".managed-candidate.json"

type diskEnvelope struct {
	Version        int             `json:"version"`
	InstallationID string          `json:"installation_id"`
	Revision       uint64          `json:"revision"`
	CommitID       string          `json:"commit_id"`
	Payload        json.RawMessage `json:"payload"`
}

// PrivateStateStore must be opened explicitly under host ownership. Constructors
// elsewhere remain I/O-free. It serializes commits and freezes after an uncertain
// rename until Recover proves the selected generation durable.
type PrivateStateStore struct {
	mu           sync.Mutex
	installation string
	files        *managedFiles
	current      ManagedState
	pending      *ManagedState
	closed       bool
}

var _ ManagedStateRepository = (*PrivateStateStore)(nil)

func storageFailure() error                         { return api.ManagementFailure(api.CodeStorageUnavailable) }
func unknownSave() error                            { return api.ManagementFailure(api.CodeSaveUnknown) }
func cloneManagedState(s ManagedState) ManagedState { s.Payload = bytes.Clone(s.Payload); return s }

func OpenPrivateState(ctx context.Context, directory, installation string) (*PrivateStateStore, error) {
	if !config.ValidID(installation) {
		return nil, invalid()
	}
	files, err := openManagedFiles(directory)
	if err != nil {
		return nil, storageFailure()
	}
	s := &PrivateStateStore{installation: installation, files: files}
	if err = files.bind(installation); err != nil {
		_ = files.close()
		return nil, storageFailure()
	}
	state, exists, err := s.read()
	if err == nil {
		err = s.barrier(exists)
	}
	if err == nil {
		err = s.cleanCandidate()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = files.close()
		return nil, storageFailure()
	}
	s.current = state
	return s, nil
}
func (s *PrivateStateStore) decode(body []byte) (ManagedState, error) {
	if len(body) > maxManagedStateBytes || !uniqueJSON(body) {
		return ManagedState{}, storageFailure()
	}
	var e diskEnvelope
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF || e.Version != managedSchemaVersion || e.InstallationID != s.installation || e.Revision == 0 || !config.ValidID(e.CommitID) || !json.Valid(e.Payload) {
		return ManagedState{}, storageFailure()
	}
	return ManagedState{Revision: e.Revision, CommitID: e.CommitID, Payload: bytes.Clone(e.Payload)}, nil
}
func (s *PrivateStateStore) read() (ManagedState, bool, error) {
	body, exists, err := s.files.read(managedStateName)
	if err != nil || !exists {
		return ManagedState{}, exists, err
	}
	state, err := s.decode(body)
	clear(body)
	return state, true, err
}
func (s *PrivateStateStore) barrier(exists bool) error {
	if exists {
		if err := s.files.syncFile(managedStateName); err != nil {
			return err
		}
	}
	return s.files.syncDir()
}
func (s *PrivateStateStore) cleanCandidate() error {
	body, exists, err := s.files.read(managedCandidateName)
	if err != nil || !exists {
		return err
	}
	// The fixed, private candidate is never authoritative. A crash may leave it
	// empty or partially written; only the named state must pass envelope/schema
	// validation. read still enforces ownership, mode, type, link and size bounds.
	clear(body)
	if err = s.files.remove(managedCandidateName); err != nil {
		return err
	}
	return s.files.syncDir()
}
func (s *PrivateStateStore) Load(ctx context.Context) (ManagedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ManagedState{}, err
	}
	if s.closed {
		return ManagedState{}, storageFailure()
	}
	if s.pending != nil {
		return ManagedState{}, unknownSave()
	}
	return cloneManagedState(s.current), nil
}
func (s *PrivateStateStore) Commit(ctx context.Context, expected uint64, state ManagedState) (CommitOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CommitNotCommitted, err
	}
	if s.closed {
		return CommitNotCommitted, storageFailure()
	}
	if s.pending != nil {
		return CommitUnknown, unknownSave()
	}
	if expected != s.current.Revision || state.Revision != expected+1 || state.Revision == 0 || !config.ValidID(state.CommitID) || state.CommitID == s.current.CommitID || !json.Valid(state.Payload) {
		return CommitNotCommitted, invalid()
	}
	body, err := json.Marshal(diskEnvelope{managedSchemaVersion, s.installation, state.Revision, state.CommitID, state.Payload})
	if err != nil || len(body) > maxManagedStateBytes {
		return CommitNotCommitted, invalid()
	}
	defer clear(body)
	candidate, err := s.decode(body)
	if err != nil {
		return CommitNotCommitted, invalid()
	}
	defer clear(candidate.Payload)
	if err = s.files.writeCandidate(body); err != nil {
		return CommitNotCommitted, storageFailure()
	}
	if err = ctx.Err(); err != nil {
		if s.files.remove(managedCandidateName) != nil {
			return CommitNotCommitted, storageFailure()
		}
		return CommitNotCommitted, err
	}
	pending := cloneManagedState(candidate)
	s.pending = &pending
	// From rename onwards context cancellation cannot skip the durability barrier.
	if err = s.files.rename(); err != nil {
		return CommitUnknown, unknownSave()
	}
	if err = s.files.syncDir(); err != nil {
		return CommitUnknown, unknownSave()
	}
	s.promote(candidate)
	return CommitDurable, nil
}
func (s *PrivateStateStore) promote(state ManagedState) {
	clear(s.current.Payload)
	s.current = cloneManagedState(state)
	if s.pending != nil {
		clear(s.pending.Payload)
		s.pending = nil
	}
}
func sameManagedState(a, b ManagedState) bool {
	return a.Revision == b.Revision && a.CommitID == b.CommitID && bytes.Equal(a.Payload, b.Payload)
}
func (s *PrivateStateStore) Recover(ctx context.Context) (ManagedState, CommitOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ManagedState{}, CommitUnknown, err
	}
	if s.closed {
		return ManagedState{}, CommitUnknown, storageFailure()
	}
	if s.pending == nil {
		return cloneManagedState(s.current), CommitDurable, nil
	}
	selected, exists, err := s.read()
	if err != nil {
		return ManagedState{}, CommitUnknown, unknownSave()
	}
	defer clear(selected.Payload)
	candidate := sameManagedState(selected, *s.pending)
	if !candidate && !sameManagedState(selected, s.current) {
		return ManagedState{}, CommitUnknown, unknownSave()
	}
	if s.barrier(exists) != nil {
		return ManagedState{}, CommitUnknown, unknownSave()
	}
	if s.cleanCandidate() != nil {
		return ManagedState{}, CommitUnknown, unknownSave()
	}
	s.promote(selected)
	if candidate {
		return cloneManagedState(s.current), CommitDurable, nil
	}
	return cloneManagedState(s.current), CommitNotCommitted, nil
}
func (s *PrivateStateStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	clear(s.current.Payload)
	if s.pending != nil {
		clear(s.pending.Payload)
	}
	if s.files.close() != nil {
		return storageFailure()
	}
	return nil
}

// Reject duplicate object keys rather than silently selecting one credential,
// installation identity or revision from an ambiguous private envelope.
func uniqueJSON(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
