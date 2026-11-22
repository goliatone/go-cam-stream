package viewer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

// CameraSettings answers only the manager who created a dashboard-added camera.
// It returns the saved address as entered and never credentials; ordinary
// catalog and status reads stay address-free.
func (s *CameraManagementService) CameraSettings(ctx context.Context, id string) (api.CameraSettings, error) {
	if err := s.options.Policy.Require(ctx); err != nil {
		return api.CameraSettings{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return api.CameraSettings{}, err
	}
	defer s.release()
	record, err := s.settingsTarget(ctx, cs.CameraID(id))
	if err != nil {
		return api.CameraSettings{}, err
	}
	authentication := api.AuthenticationAnonymous
	if record.CredentialID != "" {
		authentication = api.AuthenticationCredentials
	}
	return api.CameraSettings{CameraID: string(record.ID), Name: record.Name,
		Endpoint:    endpointString(record.Binding.Host, record.Binding.Port, record.Path, record.Query),
		DialAddress: record.Binding.Selected, Authentication: authentication,
		CatalogRevision: s.options.Viewer.CatalogRevision()}, nil
}

// settingsTarget applies the sign-in rule to every saved-camera setting: the
// enabled manager who created the camera, with its view grant. Server cameras
// and unknown IDs are indistinguishable from denial.
func (s *CameraManagementService) settingsTarget(ctx context.Context, id cs.CameraID) (ManagedRecord, error) {
	if !s.options.Viewer.canUpdateCredentials(ctx, id) {
		return ManagedRecord{}, denied()
	}
	for _, record := range s.durable.Cameras {
		if record.ID == id {
			return record, nil
		}
	}
	return ManagedRecord{}, denied()
}

// RenameCamera changes only the display name. The source key leaves the name
// out, so live sessions and the source are untouched.
func (s *CameraManagementService) RenameCamera(ctx context.Context, id string, r api.RenameCameraRequest) (api.CameraChange, error) {
	if err := r.Validate(); err != nil {
		return api.CameraChange{}, err
	}
	camera := cs.CameraID(id)
	fingerprint := changeFingerprint(api.OperationRename, camera, r)
	if change, found, err := s.replayChange(ctx, r.OperationKey, api.OperationRename, camera, fingerprint); found {
		return change, err
	}
	value, err := s.operation(ctx, r.Mutation, api.OperationRename, id, r, func() (any, error) {
		if s.options.Repository == nil {
			return nil, storageFailure()
		}
		record, err := s.settingsTarget(ctx, camera)
		if err != nil {
			return nil, err
		}
		if r.CatalogRevision != s.options.Viewer.CatalogRevision() {
			return nil, api.ManagementFailure(api.CodeCatalogConflict)
		}
		if !validCameraName(r.Name) {
			return nil, invalid()
		}
		if r.Name == record.Name {
			return s.changeResponse(camera, false, 0), nil
		}
		next := copyManagedCatalog(s.durable)
		if err = pruneReceipts(&next); err != nil {
			next.Destroy()
			return nil, err
		}
		i := slices.IndexFunc(next.Cameras, func(c ManagedRecord) bool { return c.ID == camera })
		next.Cameras[i].Name = r.Name
		next.Receipts = append(next.Receipts, ManagedReceipt{OperationKey: r.OperationKey, Owner: principal(ctx), CandidateID: camera, Kind: api.OperationRename, Fingerprint: fingerprint, ExpiresAt: time.Now().Add(api.SaveReceiptLifetime)})
		pending, err := s.prepareChange(ctx, next, api.OperationRename, r.OperationKey, catalogChange{camera: camera, rename: true})
		if err != nil {
			return nil, err
		}
		return s.commitChange(ctx, pending)
	})
	if err != nil {
		return api.CameraChange{}, err
	}
	return value.(api.CameraChange), nil
}

func changeFingerprint(kind api.OperationKind, camera cs.CameraID, request any) string {
	body, _ := json.Marshal(request)
	sum := sha256.Sum256(append([]byte(string(kind)+"\x00"+string(camera)+"\x00"), body...))
	return hex.EncodeToString(sum[:])
}

func (s *CameraManagementService) changeResponse(camera cs.CameraID, removed bool, stopped int) api.CameraChange {
	return api.CameraChange{CameraID: string(camera), CatalogRevision: s.options.Viewer.CatalogRevision(), Removed: removed, StoppedSessions: stopped}
}

// replayChange answers a repeated rename or removal from its durable receipt.
// found is false when the key has no receipt and the mutation may proceed.
func (s *CameraManagementService) replayChange(ctx context.Context, key string, kind api.OperationKind, camera cs.CameraID, fingerprint string) (api.CameraChange, bool, error) {
	if err := s.acquire(ctx); err != nil {
		return api.CameraChange{}, true, err
	}
	defer s.release()
	if s.pendingSave.matches(principal(ctx), key) && s.options.Policy.CanManage(ctx) {
		// If recovery is still pending, the operation cache checks the request
		// fingerprint and returns the original outcome.
		_ = s.recoverSave(ctx)
	}
	receipt := s.receipt(principal(ctx), key)
	if receipt == nil {
		for _, c := range s.durable.Cameras {
			if c.Creator == principal(ctx) && c.CreationKey == key {
				return api.CameraChange{}, true, api.ManagementFailure(api.CodeOperationConflict)
			}
		}
		return api.CameraChange{}, false, nil
	}
	if err := s.options.Policy.Require(ctx); err != nil {
		return api.CameraChange{}, true, err
	}
	if receipt.Kind != kind || receipt.CandidateID != camera || receipt.Fingerprint != fingerprint {
		return api.CameraChange{}, true, api.ManagementFailure(api.CodeOperationConflict)
	}
	if !time.Now().Before(receipt.ExpiresAt) {
		return api.CameraChange{}, true, api.ManagementFailure(api.CodeReceiptExpired)
	}
	return s.changeResponse(camera, kind == api.OperationRemove, 0), true, nil
}

// prepareChange builds the next generation for a change to a saved camera and
// checks it against publication's rules before anything is written. It owns
// next from here and destroys it on failure.
func (s *CameraManagementService) prepareChange(ctx context.Context, next ManagedCatalog, kind api.OperationKind, key string, change catalogChange) (*pendingCameraSave, error) {
	merged, err := MergeManagedCatalog(ctx, s.options.Base, next, nil, s.options.MaxSources)
	if err == nil {
		revision := s.options.Viewer.CatalogRevision()
		revision.Generation++
		if err = s.options.Viewer.checkCatalog(merged, revision, s.options.Policy.Managers(), change); err == nil {
			body, e := json.Marshal(next)
			if e != nil {
				err = storageFailure()
			} else {
				return &pendingCameraSave{owner: principal(ctx), key: key, kind: kind, change: change, state: ManagedState{Revision: s.stateRevision + 1, CommitID: randomManagementID(), Payload: body}, catalog: next, merged: merged, revision: revision}, nil
			}
		}
	}
	next.Destroy()
	return nil, err
}

// commitChange makes a prepared change durable and publishes it. A commit that
// did not happen is abandoned; an uncertain one stays pending for recovery.
func (s *CameraManagementService) commitChange(ctx context.Context, p *pendingCameraSave) (api.CameraChange, error) {
	s.pendingSave = p
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	outcome, err := s.options.Repository.Commit(commitCtx, s.stateRevision, p.state)
	if outcome == CommitNotCommitted {
		s.abandonSave()
		if err == nil {
			err = storageFailure()
		}
		return api.CameraChange{}, err
	}
	if outcome != CommitDurable || err != nil {
		return api.CameraChange{}, unknownSave()
	}
	if err = s.publishSave(); err != nil {
		return api.CameraChange{}, unknownSave()
	}
	return s.changeResponse(p.change.camera, p.change.remove, p.stopped), nil
}

// RemoveCamera retires a saved camera, its source, sign-in and identity. A
// watched camera is refused unless StopViewers asks to close every session.
func (s *CameraManagementService) RemoveCamera(ctx context.Context, id string, r api.RemoveCameraRequest) (api.CameraChange, error) {
	if err := r.Validate(); err != nil {
		return api.CameraChange{}, err
	}
	camera := cs.CameraID(id)
	fingerprint := changeFingerprint(api.OperationRemove, camera, r)
	if change, found, err := s.replayChange(ctx, r.OperationKey, api.OperationRemove, camera, fingerprint); found {
		return change, err
	}
	value, err := s.operation(ctx, r.Mutation, api.OperationRemove, id, r, func() (any, error) {
		if s.options.Repository == nil {
			return nil, storageFailure()
		}
		record, err := s.settingsTarget(ctx, camera)
		if err != nil {
			return nil, err
		}
		if r.CatalogRevision != s.options.Viewer.CatalogRevision() {
			return nil, api.ManagementFailure(api.CodeCatalogConflict)
		}
		next := copyManagedCatalog(s.durable)
		if err = pruneReceipts(&next); err != nil {
			next.Destroy()
			return nil, err
		}
		next.Cameras = slices.DeleteFunc(next.Cameras, func(c ManagedRecord) bool { return c.ID == camera })
		next.Credentials = slices.DeleteFunc(next.Credentials, func(c ManagedCredential) bool {
			if c.ID != record.CredentialID {
				return false
			}
			clear(c.Username)
			clear(c.Password)
			return true
		})
		next.Receipts = slices.DeleteFunc(next.Receipts, func(receipt ManagedReceipt) bool { return receipt.CandidateID == camera })
		now := time.Now()
		next.Removed = append(next.Removed, ManagedTombstone{ID: camera, Creator: record.Creator, CreationKey: record.CreationKey, RemovedAt: now})
		next.Receipts = append(next.Receipts, ManagedReceipt{OperationKey: r.OperationKey, Owner: principal(ctx), CandidateID: camera, Kind: api.OperationRemove, Fingerprint: fingerprint, ExpiresAt: now.Add(api.SaveReceiptLifetime)})
		pending, err := s.prepareChange(ctx, next, api.OperationRemove, r.OperationKey, catalogChange{camera: camera, remove: true})
		if err != nil {
			return nil, err
		}
		pending.releaseAdmission, pending.oldSource, pending.stopped, err = s.retireSource(ctx, record, r.StopViewers)
		if err != nil {
			pending.catalog.Destroy()
			clear(pending.state.Payload)
			return nil, err
		}
		return s.commitChange(ctx, pending)
	})
	if err != nil {
		return api.CameraChange{}, err
	}
	return value.(api.CameraChange), nil
}

// retireSource fences new sessions and parks the camera's source. With stop it
// first closes every live session and reports how many; without it a watched
// camera is busy. The caller releases the fence once the change is resolved.
func (s *CameraManagementService) retireSource(ctx context.Context, record ManagedRecord, stop bool) (func(), cs.SourceKey, int, error) {
	camera, err := compileManaged(ctx, record)
	if err != nil {
		return nil, "", 0, err
	}
	release, err := s.options.Viewer.beginSourceChange(record.ID)
	if err != nil {
		return nil, "", 0, err
	}
	key := camera.Sources["default"]
	stopped := 0
	if stop {
		ids, e := s.options.Core.ResetSource(ctx, key)
		if e != nil && cs.DescribeError(e).Code != cs.CodeNotFound {
			release()
			return nil, "", 0, safeError(e)
		}
		stopped = len(ids)
	}
	if err = s.options.Core.PrepareInactiveSource(ctx, camera.Camera, "default"); err != nil {
		release()
		return nil, "", 0, err
	}
	return release, key, stopped, nil
}
