package viewer

import (
	"context"
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type reconnectOperation struct {
	ref     api.StreamRef
	owner   string
	expires time.Time
	status  api.OperationResult
	result  api.ReconnectResponse
	err     error
}
type CameraRecoveryService struct {
	mu         sync.Mutex
	viewer     *CameraService
	epoch      string
	reset      func(context.Context, api.StreamRef) (api.ReconnectResponse, error)
	operations map[string]*reconnectOperation
	active     map[cs.SourceKey]bool
}

var _ CameraRecovery = (*CameraRecoveryService)(nil)

func NewCameraRecoveryService(service *CameraService, epoch string, reset func(context.Context, api.StreamRef) (api.ReconnectResponse, error)) (*CameraRecoveryService, error) {
	if service == nil || epoch == "" || reset == nil {
		return nil, invalid()
	}
	return &CameraRecoveryService{viewer: service, epoch: epoch, reset: reset, operations: map[string]*reconnectOperation{}, active: map[cs.SourceKey]bool{}}, nil
}
func (s *CameraRecoveryService) authorize(ctx context.Context, ref api.StreamRef) (cs.SourceKey, error) {
	ctx = s.viewer.captureCatalog(ctx)
	item, err := s.viewer.require(ctx, ref.CameraID, ref.StreamID, reconnect)
	if err != nil {
		return "", err
	}
	key := item.Sources[ref.StreamID]
	for _, alias := range s.viewer.snapshot(ctx).aliases[key] {
		if _, err = s.viewer.require(ctx, alias.CameraID, alias.StreamID, reconnect); err != nil {
			return "", err
		}
	}
	return key, nil
}
func (s *CameraRecoveryService) RecoverCamera(ctx context.Context, ref api.StreamRef, m api.Mutation) (api.ReconnectResponse, error) {
	if err := m.Validate(); err != nil {
		return api.ReconnectResponse{}, err
	}
	key, err := s.authorize(ctx, ref)
	if err != nil {
		return api.ReconnectResponse{}, err
	}
	if m.RuntimeEpoch != s.epoch {
		return api.ReconnectResponse{}, api.ManagementFailure(api.CodeRecoveryLost)
	}
	s.mu.Lock()
	operationKey := principal(ctx) + "\x00" + m.OperationKey
	now := time.Now()
	if old := s.operations[operationKey]; old != nil {
		if old.ref != ref {
			s.mu.Unlock()
			return api.ReconnectResponse{}, api.ManagementFailure(api.CodeOperationConflict)
		}
		if now.After(old.expires) {
			s.mu.Unlock()
			return api.ReconnectResponse{}, api.ManagementFailure(api.CodeRecoveryLost)
		}
		if old.status.State == api.OperationProcessing {
			s.mu.Unlock()
			return api.ReconnectResponse{}, api.ManagementFailure(api.CodeOperationInProgress)
		}
		result, err := cloneReconnect(old.result), old.err
		s.mu.Unlock()
		return result, err
	}
	for k, op := range s.operations {
		if op.status.State != api.OperationProcessing && now.After(op.expires) {
			delete(s.operations, k)
		}
	}
	if len(s.operations) >= api.MaxRecoveryOperations || s.active[key] {
		s.mu.Unlock()
		return api.ReconnectResponse{}, cs.NewError(cs.CodeCapacityExceeded, nil)
	}
	op := &reconnectOperation{ref: ref, owner: principal(ctx), expires: now.Add(api.RecoveryLifetime), status: api.OperationResult{OperationKey: m.OperationKey, Kind: api.OperationRecovery, State: api.OperationProcessing, RuntimeEpoch: s.epoch}}
	s.operations[operationKey] = op
	s.active[key] = true
	s.mu.Unlock()
	// Reconnect owns authorization, alias impact and reset behavior; do not copy it.
	result, err := s.reset(ctx, ref)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, key)
	op.result, op.err = cloneReconnect(result), err
	op.status.State = api.OperationSucceeded
	if err != nil {
		op.status.State = api.OperationNotCommitted
		body := api.ManagementPublicError(err).Error
		op.status.Error = &body
	} else {
		copy := cloneReconnect(result)
		op.status.Recovery = &copy
	}
	return result, err
}
func cloneReconnect(r api.ReconnectResponse) api.ReconnectResponse {
	r.Affected = append([]api.StreamRef{}, r.Affected...)
	r.ClosedSessions = append([]cs.SessionID{}, r.ClosedSessions...)
	return r
}
func (s *CameraRecoveryService) RecoveryOperation(ctx context.Context, key, epoch string) (api.OperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.epoch {
		return api.OperationResult{}, api.ManagementFailure(api.CodeRecoveryLost)
	}
	op := s.operations[principal(ctx)+"\x00"+key]
	if op == nil {
		return api.OperationResult{OperationKey: key, Kind: api.OperationRecovery, State: api.OperationNotFound, RuntimeEpoch: s.epoch}, nil
	}
	if _, err := s.authorize(ctx, op.ref); err != nil {
		return api.OperationResult{}, err
	}
	if time.Now().After(op.expires) {
		return api.OperationResult{}, api.ManagementFailure(api.CodeRecoveryLost)
	}
	out := op.status
	if out.Recovery != nil {
		copy := cloneReconnect(*out.Recovery)
		out.Recovery = &copy
	}
	return out, nil
}
