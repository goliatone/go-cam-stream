package viewer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

var _ CameraManagement = (*CameraManagementService)(nil)

type pendingCameraSave struct {
	owner, key, draft string
	state             ManagedState
	catalog           ManagedCatalog
	merged            config.Catalog
	revision          api.CatalogRevision
	promoted          bool
}

func copyManagedCatalog(m ManagedCatalog) ManagedCatalog {
	body, _ := json.Marshal(m)
	defer clear(body)
	var out ManagedCatalog
	_ = json.Unmarshal(body, &out)
	if out.Cameras == nil {
		out.Cameras = []ManagedRecord{}
	}
	if out.Credentials == nil {
		out.Credentials = []ManagedCredential{}
	}
	if out.Receipts == nil {
		out.Receipts = []ManagedReceipt{}
	}
	return out
}

// InitializeManagedState is a host-lifetime operation, before HTTP admission.
// The repository must already have established durability under its ownership lock.
func (s *CameraManagementService) InitializeManagedState(ctx context.Context, state ManagedState, repository ...ManagedStateRepository) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	if len(s.drafts) != 0 || len(s.operations) != 0 || s.options.Viewer.CatalogRevision().RuntimeEpoch != "" {
		return invalid()
	}
	managed, err := DecodeManagedCatalog(state)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			managed.Destroy()
		}
	}()
	bindings, err := s.resolveBaseBindings(ctx)
	if err != nil && len(managed.Cameras) > 0 {
		return err
	}
	merged, err := MergeManagedCatalog(ctx, s.options.Base, managed, bindings, s.options.MaxSources)
	if err != nil {
		return err
	}
	identities := NewIdentityIndex()
	for _, r := range managed.Cameras {
		if err = identities.Observe(string(r.ID), r.Binding); err != nil {
			return err
		}
	}
	if err = s.options.Viewer.PublishCatalog(merged, api.CatalogRevision{RuntimeEpoch: s.epoch, Generation: 1}, s.options.Policy.Managers(), func() error { return s.options.Secrets.Publish(managed.Credentials) }); err != nil {
		return err
	}
	if len(repository) > 0 {
		s.options.Repository = repository[0]
	}
	s.durable.Destroy()
	s.durable = managed
	s.stateRevision = state.Revision
	s.identities = identities
	keep = true
	return nil
}

func saveFingerprint(r api.SaveCameraRequest) string {
	body, _ := json.Marshal(r)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (s *CameraManagementService) savedCamera(ctx context.Context, key string) (*ManagedRecord, error) {
	for i := range s.durable.Cameras {
		r := &s.durable.Cameras[i]
		if r.Creator == principal(ctx) && r.CreationKey == key {
			if _, err := s.options.Viewer.require(ctx, r.ID, "default", view); err != nil {
				return nil, err
			}
			return r, nil
		}
	}
	return nil, nil
}
func (s *CameraManagementService) saveResponse(r *ManagedRecord) api.SaveCameraResponse {
	return api.SaveCameraResponse{CameraID: string(r.ID), CatalogRevision: s.options.Viewer.CatalogRevision()}
}
func (s *CameraManagementService) replaySave(ctx context.Context, r api.SaveCameraRequest) (api.SaveCameraResponse, bool, error) {
	record, err := s.savedCamera(ctx, r.OperationKey)
	if err != nil {
		return api.SaveCameraResponse{}, true, err
	}
	if record == nil {
		return api.SaveCameraResponse{}, false, nil
	}
	for _, receipt := range s.durable.Receipts {
		if receipt.Owner == principal(ctx) && receipt.OperationKey == r.OperationKey {
			if receipt.Fingerprint != saveFingerprint(r) {
				return api.SaveCameraResponse{}, true, api.ManagementFailure(api.CodeOperationConflict)
			}
			if time.Now().Before(receipt.ExpiresAt) {
				return s.saveResponse(record), true, nil
			}
		}
	}
	return api.SaveCameraResponse{}, true, api.ManagementFailure(api.CodeReceiptExpired)
}

func (s *CameraManagementService) SaveCamera(ctx context.Context, r api.SaveCameraRequest) (api.SaveCameraResponse, error) {
	if err := r.Mutation.Validate(); err != nil {
		return api.SaveCameraResponse{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return api.SaveCameraResponse{}, err
	}
	err := s.recoverSave(ctx)
	if err == nil {
		response, found, replayErr := s.replaySave(ctx, r)
		if found {
			s.release()
			return response, replayErr
		}
	}
	s.release()
	if err != nil {
		return api.SaveCameraResponse{}, err
	}
	if err = s.options.Policy.Require(ctx); err != nil {
		return api.SaveCameraResponse{}, err
	}
	// Cached failures/replies need no DNS. New mutations resolve outside the gate.
	s.opMu.Lock()
	known := s.operations[principal(ctx)+"\x00"+r.OperationKey] != nil
	s.opMu.Unlock()
	var bindings []AddressBinding
	observed := time.Now()
	if !known {
		bindings, err = s.resolveBaseBindings(ctx)
		if err != nil {
			return api.SaveCameraResponse{}, err
		}
		observed = time.Now()
	}
	value, err := s.operation(ctx, r.Mutation, api.OperationSave, r.DraftID, r, func() (any, error) {
		if s.pendingSave != nil {
			return nil, unknownSave()
		}
		if s.options.Repository == nil {
			return nil, storageFailure()
		}
		if time.Since(observed) > 5*time.Second {
			return nil, api.ManagementFailure(api.CodeIdentityUnverified)
		}
		if r.CatalogRevision != s.options.Viewer.CatalogRevision() {
			return nil, api.ManagementFailure(api.CodeCatalogConflict)
		}
		d, e := s.ownedDraft(ctx, r.DraftID)
		if e != nil {
			return nil, e
		}
		if d.review.Revision != r.ExpectedRevision {
			return nil, api.ManagementFailure(api.CodeRevisionConflict)
		}
		if d.review.State != api.DraftTested || d.review.Test == nil || d.review.Test.Cleanup != api.CleanupConfirmed || d.testOutcome != api.TestPassed || d.passedAt.IsZero() || time.Since(d.passedAt) > api.TestPassLifetime {
			return nil, api.ManagementFailure(api.CodeTestExpired)
		}
		if d.review.Authentication == api.AuthenticationCredentials && d.credential == nil {
			return nil, api.ManagementFailure(api.CodeCredentialsRequired)
		}
		if e = s.identities.Reserve(d.review.ID, d.binding); e != nil {
			return nil, e
		}
		next := copyManagedCatalog(s.durable)
		retained := false
		defer func() {
			if !retained {
				next.Destroy()
			}
		}()
		receipts := next.Receipts[:0]
		for _, receipt := range next.Receipts {
			if time.Now().Before(receipt.ExpiresAt) {
				receipts = append(receipts, receipt)
			}
		}
		next.Receipts = receipts
		if len(next.Receipts) >= api.MaxSaveReceipts {
			return nil, cs.NewError(cs.CodeCapacityExceeded, nil)
		}
		record := ManagedRecord{ID: cs.CameraID(d.review.CandidateID), PhysicalID: cs.PhysicalCameraID(d.review.CandidateID), Name: d.review.Name, Creator: d.owner, Binding: d.binding, Path: d.config.Profiles[0].Path, Query: d.config.Profiles[0].Query, CreationKey: r.OperationKey, CreatedAt: time.Now()}
		if d.credential != nil {
			record.CredentialID = d.credential.ID
			next.Credentials = append(next.Credentials, copyCredential(*d.credential))
		}
		next.Cameras = append(next.Cameras, record)
		next.Receipts = append(next.Receipts, ManagedReceipt{OperationKey: r.OperationKey, Owner: d.owner, CandidateID: record.ID, Fingerprint: saveFingerprint(r), ExpiresAt: time.Now().Add(api.SaveReceiptLifetime)})
		merged, e := MergeManagedCatalog(ctx, s.options.Base, next, bindings, s.options.MaxSources)
		if e != nil {
			return nil, e
		}
		revision := s.options.Viewer.CatalogRevision()
		revision.Generation++
		if _, e = newCatalogSnapshot(merged, revision, s.options.Policy.Managers()); e != nil {
			return nil, e
		}
		body, e := json.Marshal(next)
		if e != nil {
			return nil, storageFailure()
		}
		pending := &pendingCameraSave{owner: d.owner, key: r.OperationKey, draft: d.review.ID, state: ManagedState{Revision: s.stateRevision + 1, CommitID: randomManagementID(), Payload: body}, catalog: next, merged: merged, revision: revision}
		s.pendingSave = pending
		retained = true
		d.review.State = api.DraftSaving
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		outcome, commitErr := s.options.Repository.Commit(commitCtx, s.stateRevision, pending.state)
		if outcome == CommitNotCommitted {
			s.abandonSave()
			if commitErr == nil {
				commitErr = storageFailure()
			}
			return nil, commitErr
		}
		if outcome != CommitDurable || commitErr != nil {
			return nil, unknownSave()
		}
		if e = s.publishSave(); e != nil {
			return nil, unknownSave()
		}
		return api.SaveCameraResponse{CameraID: string(record.ID), CatalogRevision: revision}, nil
	})
	if err != nil {
		return api.SaveCameraResponse{}, err
	}
	return value.(api.SaveCameraResponse), nil
}

func (s *CameraManagementService) publishSave() error {
	p := s.pendingSave
	if p == nil {
		return nil
	}
	d := s.drafts[p.draft]
	if d == nil {
		return unknownSave()
	}
	if err := s.options.Viewer.PublishCatalog(p.merged, p.revision, s.options.Policy.Managers(), func() error {
		if err := s.options.Secrets.Publish(p.catalog.Credentials); err != nil {
			return err
		}
		if !p.promoted {
			if err := s.identities.Promote(d.review.ID, d.review.CandidateID, d.binding); err != nil {
				return err
			}
			p.promoted = true
		}
		return nil
	}); err != nil {
		return err
	}
	s.durable.Destroy()
	s.durable = p.catalog
	s.stateRevision = p.state.Revision
	clear(p.state.Payload)
	s.pendingSave = nil
	d.review.State = api.DraftSaved
	s.discardCredential(d)
	return nil
}
func (s *CameraManagementService) abandonSave() {
	p := s.pendingSave
	if p == nil {
		return
	}
	if d := s.drafts[p.draft]; d != nil {
		d.review.State = api.DraftTested
		if time.Since(d.passedAt) > api.TestPassLifetime {
			d.failTest(api.ManagementFailure(api.CodeTestExpired))
			d.review.State = api.DraftReady
			d.review.Test.Stage = api.TestFailed
			s.identities.Release(d.review.ID)
		}
		if time.Now().After(d.review.ExpiresAt) {
			d.cancelTest()
			d.review.State = api.DraftExpired
			d.review.Test.Stage = api.TestCanceled
			s.identities.Release(d.review.ID)
			s.discardCredential(d)
		}
	}
	p.catalog.Destroy()
	clear(p.state.Payload)
	s.pendingSave = nil
}
func (s *CameraManagementService) recoverSave(ctx context.Context) error {
	p := s.pendingSave
	if p == nil {
		return nil
	}
	state, outcome, err := s.options.Repository.Recover(ctx)
	defer clear(state.Payload)
	if err != nil || outcome == CommitUnknown {
		return unknownSave()
	}
	var response any
	var resultErr error
	if outcome == CommitDurable && sameManagedState(state, p.state) {
		if err = s.publishSave(); err != nil {
			return unknownSave()
		}
		record, e := s.savedCamera(WithPrincipal(ctx, p.owner), p.key)
		if e != nil || record == nil {
			return unknownSave()
		}
		response = s.saveResponse(record)
	} else if outcome == CommitNotCommitted && state.Revision == s.stateRevision {
		s.abandonSave()
		resultErr = storageFailure()
	} else {
		return unknownSave()
	}
	s.opMu.Lock()
	if op := s.operations[p.owner+"\x00"+p.key]; op != nil {
		settleManagementOperation(op, response, resultErr)
	}
	s.opMu.Unlock()
	return nil
}

// RuntimeEpoch identifies this host lifetime, including source recovery.
func (s *CameraManagementService) RuntimeEpoch() string { return s.epoch }
