package viewer

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

type ManagementOptions struct {
	Core           *cs.Service
	Viewer         *CameraService
	Policy         *ManagementPolicy
	Secrets        *ManagedSecrets
	Base           config.Catalog
	Managed        ManagedCatalog
	Repository     ManagedStateRepository
	Lookup         AddressLookup
	Epoch          string
	CleanupTimeout time.Duration
	MaxSources     int
}
type cameraDraft struct {
	owner           string
	review          api.DraftReview
	config          rtsp.Config
	credential      *ManagedCredential
	binding         AddressBinding
	camera          cs.Camera
	session         cs.SessionID
	passedAt        time.Time
	testOutcome     api.TestStage // terminal intent, independent of fallible cleanup
	testCancel      context.CancelFunc
	cancelRequested bool
}
type managementOperation struct {
	owner, target string
	digest        [32]byte
	status        api.OperationResult
	result        any
	err           error
	expires       time.Time
	recovery      *pendingProbeCleanup
}
type CameraManagementService struct {
	gate          chan struct{} // transition/commit serialization; never the viewer catalog lock
	opMu          sync.Mutex
	options       ManagementOptions
	epoch         string
	digestKey     [32]byte
	drafts        map[string]*cameraDraft
	operations    map[string]*managementOperation
	identities    *IdentityIndex
	durable       ManagedCatalog
	stateRevision uint64
	pendingSave   *pendingCameraSave
	stopped       atomic.Bool
	cancel        context.CancelFunc
	done          chan struct{}
}

func randomManagementID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func NewCameraManagementService(options ManagementOptions) (*CameraManagementService, error) {
	if options.Core == nil || options.Viewer == nil || options.Policy == nil || options.Secrets == nil || options.CleanupTimeout <= 0 || options.MaxSources < 1 {
		return nil, invalid()
	}
	epoch := options.Epoch
	if epoch == "" {
		epoch = randomManagementID()
	}
	if epoch == "" {
		return nil, invalid()
	}
	s := &CameraManagementService{gate: make(chan struct{}, 1), options: options, epoch: epoch, drafts: map[string]*cameraDraft{}, operations: map[string]*managementOperation{}, identities: NewIdentityIndex(), durable: copyManagedCatalog(options.Managed)}
	if _, err := rand.Read(s.digestKey[:]); err != nil {
		return nil, invalid()
	}
	for _, r := range options.Managed.Cameras {
		if err := s.identities.Observe(string(r.ID), r.Binding); err != nil {
			return nil, err
		}
	}
	s.options.Managed = ManagedCatalog{} // the service owns only its cloned credential buffers
	return s, nil
}
func (s *CameraManagementService) Capabilities(ctx context.Context) (api.Capabilities, error) {
	if !s.options.Viewer.AdmittedPrincipals()[principal(ctx)] && !s.options.Policy.CanManage(ctx) {
		return api.Capabilities{}, denied()
	}
	return api.Capabilities{CanManageCameras: s.options.Policy.CanManage(ctx), RecoveryScope: s.options.Policy.RecoveryScope(ctx), RuntimeEpoch: s.epoch}, nil
}
func cloneReview(in api.DraftReview) api.DraftReview {
	body, _ := json.Marshal(in)
	var out api.DraftReview
	_ = json.Unmarshal(body, &out)
	return out
}
func cloneTest(in api.TestResult) api.TestResult {
	body, _ := json.Marshal(in)
	var out api.TestResult
	_ = json.Unmarshal(body, &out)
	return out
}
func (s *CameraManagementService) operation(ctx context.Context, m api.Mutation, kind api.OperationKind, target string, payload any, fn func() (any, error)) (any, error) {
	if err := s.options.Policy.Require(ctx); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if m.RuntimeEpoch != s.epoch {
		return nil, api.ManagementFailure(api.CodeDraftLost)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, invalid()
	}
	mac := hmac.New(sha256.New, s.digestKey[:])
	_, _ = mac.Write([]byte(string(kind) + "\x00" + target + "\x00"))
	_, _ = mac.Write(encoded)
	clear(encoded)
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	owner := principal(ctx)
	key := owner + "\x00" + m.OperationKey
	s.opMu.Lock()
	if old := s.operations[key]; old != nil {
		if old.digest != digest {
			s.opMu.Unlock()
			return nil, api.ManagementFailure(api.CodeOperationConflict)
		}
		if old.status.State == api.OperationProcessing {
			s.opMu.Unlock()
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		result, err := old.result, old.err
		s.opMu.Unlock()
		return result, err
	}
	now := time.Now()
	count := 0
	for key, op := range s.operations {
		if op.status.State != api.OperationProcessing && op.status.State != api.OperationUnknown && now.After(op.expires) {
			delete(s.operations, key)
		} else if target != "" && op.target == target && op.owner == owner {
			count++
		}
	}
	if len(s.operations) >= api.MaxOperations || count >= api.MaxDraftOperations {
		s.opMu.Unlock()
		return nil, cs.NewError(cs.CodeCapacityExceeded, nil)
	}
	op := &managementOperation{owner: owner, target: target, digest: digest, expires: now.Add(api.DraftLifetime + api.TerminalRecoveryLifetime), status: api.OperationResult{OperationKey: m.OperationKey, Kind: kind, State: api.OperationProcessing, RuntimeEpoch: s.epoch}}
	s.operations[key] = op
	s.opMu.Unlock()
	var result any
	err = s.acquire(ctx)
	if err == nil {
		// Publish the result before releasing the transition gate. A cleanup
		// sweep must never race an operation's transition to outcome_unknown.
		defer s.release()
		if s.stopped.Load() {
			err = cs.NewError(cs.CodeServerUnavailable, nil)
		} else if err = ctx.Err(); err == nil {
			result, err = fn()
		}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	pending, recovering := result.(pendingProbeCleanup)
	if recovering {
		result = nil
	}
	settleManagementOperation(op, result, err)
	if kind == api.OperationSave && api.ManagementPublicError(err).Error.Code == string(api.CodeSaveUnknown) {
		op.status.State = api.OperationUnknown
	}
	if recovering && cs.DescribeError(err).Code == cs.CodeCleanupIncomplete {
		op.recovery = &pending
		op.status.State = api.OperationUnknown
	}
	return result, err
}

// Only a mutation that reached its owned cleanup step may request recovery.
// Validation/expiry cleanup before a mutation is not an uncertain mutation.
type pendingProbeCleanup struct {
	testID   string
	revision uint64
}

func pendingCleanup(d *cameraDraft) pendingProbeCleanup {
	return pendingProbeCleanup{testID: d.review.Test.ID, revision: d.review.Revision}
}

// Called under opMu for both immediate results and recovered outcomes.
func settleManagementOperation(op *managementOperation, result any, err error) {
	op.result, op.err, op.recovery = result, err, nil
	op.status.State, op.status.Error = api.OperationSucceeded, nil
	if err != nil {
		body := api.ManagementPublicError(err).Error
		op.status.Error = &body
		op.status.State = api.OperationNotCommitted
	}
	switch v := result.(type) {
	case api.DraftReview:
		op.status.DraftID = v.ID
		op.status.CandidateID = v.CandidateID
		op.status.Revision = v.Revision
	case api.TestResult:
		op.status.DraftID = op.target
		op.status.TestID = v.ID
		op.status.Revision = v.DraftRevision
	case api.SaveCameraResponse:
		op.status.Saved = &v
	}
}

func (s *CameraManagementService) CreateDraft(ctx context.Context, r api.CreateDraftRequest) (api.DraftReview, error) {
	value, err := s.operation(ctx, r.Mutation, api.OperationCreate, "", r, func() (any, error) { return s.createDraft(ctx, r.DraftInput, false) })
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(value.(api.DraftReview)), nil
}
func (s *CameraManagementService) ImportDraft(ctx context.Context, r api.ImportDraftRequest) (api.DraftReview, error) {
	value, err := s.operation(ctx, r.Mutation, api.OperationImport, "", r, func() (any, error) {
		input, err := importCamera(r)
		if err != nil {
			return nil, err
		}
		return s.createDraft(ctx, input, true)
	})
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(value.(api.DraftReview)), nil
}
func (s *CameraManagementService) createDraft(ctx context.Context, input api.DraftInput, imported bool) (api.DraftReview, error) {
	input, cfg, err := normalizeDraft(input)
	if err != nil {
		return api.DraftReview{}, err
	}
	owner := principal(ctx)
	count, total := 0, 0
	for _, d := range s.drafts {
		if d.review.State != api.DraftCanceled && d.review.State != api.DraftExpired && d.review.State != api.DraftSaved {
			total++
			if d.owner == owner {
				count++
			}
		}
	}
	// Terminal drafts also consume bounded memory until their recovery expiry.
	if count >= api.MaxOwnerDrafts || total >= api.MaxDrafts || len(s.drafts) >= api.MaxDrafts*2 {
		return api.DraftReview{}, cs.NewError(cs.CodeCapacityExceeded, nil)
	}
	id, candidate := randomManagementID(), randomManagementID()
	if id == "" || candidate == "" {
		return api.DraftReview{}, invalid()
	}
	d := &cameraDraft{owner: owner, config: cfg, review: api.DraftReview{ID: id, CandidateID: candidate, Revision: 1, RuntimeEpoch: s.epoch, ExpiresAt: time.Now().Add(api.DraftLifetime), Name: input.Name, Endpoint: input.Endpoint, Authentication: input.Authentication, Warnings: []api.DraftWarning{api.WarningInitialAddressOnly, api.WarningPrivateAccess}, State: api.DraftReady}}
	if cfg.Host == "localhost" || cfg.Host == "127.0.0.1" || cfg.Host == "::1" {
		d.review.Warnings = append(d.review.Warnings, api.WarningServerLocalhost)
	}
	if imported {
		d.review.Warnings = append(d.review.Warnings, api.WarningIgnoredGrants)
	}
	if err = s.replaceCredentials(d, input); err != nil {
		return api.DraftReview{}, err
	}
	s.drafts[id] = d
	return cloneReview(d.review), nil
}
func (s *CameraManagementService) replaceCredentials(d *cameraDraft, input api.DraftInput) error {
	if input.Authentication == api.AuthenticationAnonymous || input.Credentials != nil {
		s.discardCredential(d)
	}
	if input.Credentials != nil {
		c := ManagedCredential{ID: randomManagementID(), Username: []byte(input.Credentials.Username), Password: []byte(input.Credentials.Password)}
		if err := s.options.Secrets.PutDraft(c); err != nil {
			clear(c.Username)
			clear(c.Password)
			return err
		}
		d.credential = &c
	}
	d.review.CredentialsPresent = d.credential != nil
	if input.Authentication == api.AuthenticationCredentials && d.credential == nil {
		d.review.State = api.DraftIncomplete
	} else {
		d.review.State = api.DraftReady
	}
	return nil
}
func (s *CameraManagementService) discardCredential(d *cameraDraft) {
	if d.credential != nil {
		s.options.Secrets.RemoveDraft(d.credential.ID)
		clear(d.credential.Username)
		clear(d.credential.Password)
		d.credential = nil
	}
}
func (s *CameraManagementService) ownedDraft(ctx context.Context, id string) (*cameraDraft, error) {
	d := s.drafts[id]
	if d == nil || d.owner != principal(ctx) {
		return nil, denied()
	}
	if time.Now().After(d.review.ExpiresAt) && d.review.State != api.DraftSaving && d.review.State != api.DraftSaved {
		if err := s.cleanupDraft(d); err != nil {
			return nil, err
		}
		d.review.State = api.DraftExpired
		s.discardCredential(d)
		return nil, api.ManagementFailure(api.CodeDraftExpired)
	}
	return d, nil
}
func (s *CameraManagementService) Draft(ctx context.Context, id string) (api.DraftReview, error) {
	if err := s.options.Policy.Require(ctx); err != nil {
		return api.DraftReview{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return api.DraftReview{}, err
	}
	defer s.release()
	d, err := s.ownedDraft(ctx, id)
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(d.review), nil
}
func (s *CameraManagementService) PatchDraft(ctx context.Context, id string, r api.PatchDraftRequest) (api.DraftReview, error) {
	v, err := s.operation(ctx, r.Mutation, api.OperationPatch, id, r, func() (any, error) {
		d, err := s.ownedDraft(ctx, id)
		if err != nil {
			return nil, err
		}
		if d.review.Revision != r.ExpectedRevision {
			return nil, api.ManagementFailure(api.CodeRevisionConflict)
		}
		if d.review.State != api.DraftReady && d.review.State != api.DraftIncomplete && d.review.State != api.DraftTested {
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		input, cfg, err := normalizeDraft(r.DraftInput)
		if err != nil {
			return nil, err
		}
		s.identities.Release(id)
		d.binding = AddressBinding{}
		d.review.DialAddress = ""
		d.passedAt = time.Time{}
		d.testOutcome = ""
		d.review.Test = nil
		if err = s.replaceCredentials(d, input); err != nil {
			return nil, err
		}
		d.config = cfg
		d.review.Name = input.Name
		d.review.Endpoint = input.Endpoint
		d.review.Authentication = input.Authentication
		d.review.Revision++
		return cloneReview(d.review), nil
	})
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(v.(api.DraftReview)), nil
}
func (s *CameraManagementService) CancelDraft(ctx context.Context, id string, r api.Mutation) (api.DraftReview, error) {
	v, err := s.operation(ctx, r, api.OperationCancel, id, r, func() (any, error) {
		d, err := s.ownedDraft(ctx, id)
		if err != nil {
			return nil, err
		}
		if d.review.State == api.DraftSaving {
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		if d.review.State == api.DraftSaved {
			return cloneReview(d.review), nil
		}
		d.cancelRequested = true
		if err = s.cleanupDraft(d); err != nil {
			return pendingCleanup(d), err
		}
		s.discardCredential(d)
		d.review.State = api.DraftCanceled
		return cloneReview(d.review), nil
	})
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(v.(api.DraftReview)), nil
}
func (s *CameraManagementService) Operation(ctx context.Context, key, candidate, epoch string) (api.OperationResult, error) {
	if err := s.acquire(ctx); err != nil {
		return api.OperationResult{}, err
	}
	if err := s.recoverSave(ctx); err != nil {
		pending := s.pendingSave
		if pending != nil && pending.owner == principal(ctx) && pending.key == key && s.options.Policy.CanManage(ctx) {
			body := api.ManagementPublicError(err).Error
			s.release()
			return api.OperationResult{OperationKey: key, Kind: api.OperationSave, State: api.OperationUnknown, RuntimeEpoch: s.epoch, Error: &body}, nil
		}
		s.release()
		return api.OperationResult{}, denied()
	}
	record, saveErr := s.savedCamera(ctx, key)
	if saveErr != nil {
		s.release()
		return api.OperationResult{}, saveErr
	}
	if record != nil {
		if candidate != "" && candidate != string(record.ID) {
			s.release()
			return api.OperationResult{}, denied()
		}
		response := s.saveResponse(record)
		s.release()
		return api.OperationResult{OperationKey: key, Kind: api.OperationSave, State: api.OperationSucceeded, RuntimeEpoch: s.epoch, CandidateID: string(record.ID), Saved: &response}, nil
	}
	s.release()
	if err := s.options.Policy.Require(ctx); err != nil {
		return api.OperationResult{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	op := s.operations[principal(ctx)+"\x00"+key]
	if op == nil {
		if epoch != s.epoch {
			if candidate != "" {
				return api.OperationResult{OperationKey: key, Kind: api.OperationSave, State: api.OperationNotCommitted, RuntimeEpoch: s.epoch}, nil
			}
			return api.OperationResult{}, api.ManagementFailure(api.CodeDraftLost)
		}
		return api.OperationResult{OperationKey: key, Kind: api.OperationCreate, State: api.OperationNotFound, RuntimeEpoch: s.epoch}, nil
	}
	body, _ := json.Marshal(op.status)
	var result api.OperationResult
	_ = json.Unmarshal(body, &result)
	return result, nil
}

// Start enables bounded expiry/deadline cleanup under an explicit host lifetime.
func (s *CameraManagementService) Start(ctx context.Context) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	if s.done != nil || s.stopped.Load() {
		return invalid()
	}
	runtime, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runtime.Done():
				return
			case <-ticker.C:
				s.expire()
			}
		}
	}()
	return nil
}
func (s *CameraManagementService) expire() {
	if s.stopped.Load() {
		return
	}
	if s.acquire(context.Background()) != nil {
		return
	}
	defer s.release()
	if s.stopped.Load() {
		return
	}
	now := time.Now()
	for id, d := range s.drafts {
		expired := now.After(d.review.ExpiresAt)
		deadline := d.review.Test != nil && now.After(d.review.Test.ExpiresAt) && (d.review.State == api.DraftTesting || d.review.State == api.DraftCleanupFailed)
		if (expired || deadline) && d.review.State != api.DraftSaving && d.review.State != api.DraftSaved {
			if s.cleanupDraft(d) != nil {
				continue
			}
			if expired {
				s.discardCredential(d)
				d.review.State = api.DraftExpired
			}
		}
		if now.After(d.review.ExpiresAt.Add(api.TerminalRecoveryLifetime)) && (d.review.State == api.DraftCanceled || d.review.State == api.DraftExpired || d.review.State == api.DraftSaved) {
			delete(s.drafts, id)
		}
	}
}
func (s *CameraManagementService) Close(ctx context.Context) error {
	s.stopped.Store(true)
	if err := s.acquire(ctx); err != nil {
		return err
	}
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	if err := s.recoverSave(ctx); err != nil {
		s.release()
		return err
	}
	var cleanupErr error
	for _, d := range s.drafts {
		if d.review.State == api.DraftSaved {
			continue
		}
		if cleanupErr = ctx.Err(); cleanupErr != nil {
			break
		}
		d.cancelRequested = true
		if cleanupErr = s.cleanupDraft(d); cleanupErr != nil {
			break
		}
		s.discardCredential(d)
	}
	if cleanupErr == nil {
		s.durable.Destroy()
	}
	s.release()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return cleanupErr
}

func (s *CameraManagementService) acquire(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *CameraManagementService) release() { <-s.gate }
