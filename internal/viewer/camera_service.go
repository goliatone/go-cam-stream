package viewer

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

// CameraService adds immutable startup catalog/access policy to the public
// coordinator. It owns principal bindings, not another media lifecycle engine.
type CameraService struct {
	core         *cs.Service
	timeout      time.Duration
	catalogState atomic.Pointer[catalogSnapshot]
	publication  sync.Mutex
	mu           sync.Mutex
	owners       map[cs.SessionID]string // bounded by the core's MaxSessionRecords
}

// NewCameraService snapshots a catalog from config.Prepare. Provider-owned typed
// config values must be immutable. The composition root owns core/backend Start
// and Close; this constructor performs no backend or credential I/O.
func NewCameraService(core *cs.Service, catalog config.Catalog, timeout time.Duration) (*CameraService, error) {
	if core == nil || timeout <= 0 {
		return nil, invalid()
	}
	s := &CameraService{core: core, timeout: timeout, owners: map[cs.SessionID]string{}}
	snapshot, err := newCatalogSnapshot(catalog, api.CatalogRevision{}, nil)
	if err != nil {
		return nil, err
	}
	s.catalogState.Store(snapshot)
	return s, nil
}

func (s *CameraService) CreateSession(ctx context.Context, camera cs.CameraID, stream cs.StreamID, request api.CreateSessionRequest) (api.Session, error) {
	ctx = s.captureCatalog(ctx)
	item, err := s.require(ctx, camera, stream, view)
	if err != nil {
		return api.Session{}, err
	}
	if err = request.Validate(); err != nil {
		return api.Session{}, err
	}
	state, err := s.core.CreateSession(ctx, item.Camera, stream, cs.Offer{SDP: request.Offer.SDP})
	if state.ID != "" {
		s.mu.Lock()
		s.owners[state.ID] = principal(ctx)
		s.mu.Unlock()
	}
	out := api.Session{ID: state.ID, CameraID: camera, StreamID: stream, State: state.State}
	if err != nil {
		return out, safeError(err)
	}
	if state.Answer == nil {
		return out, cs.NewError(cs.CodeSignalingFailed, nil)
	}
	out.Playback = api.Playback{Protocol: "webrtc", Answer: api.SDPAnswer{Type: "answer", SDP: state.Answer.SDP}}
	return out, nil
}
func (s *CameraService) CloseSession(ctx context.Context, id cs.SessionID) error {
	ctx = s.captureCatalog(ctx)
	s.mu.Lock()
	owner, known := s.owners[id]
	s.mu.Unlock()
	if !known {
		return denied()
	}
	camera, stream, err := s.core.SessionBinding(id)
	if err != nil {
		return denied()
	}
	viewer := owner == principal(ctx) && s.allowed(ctx, camera, stream, view)
	operator := s.allowed(ctx, camera, stream, reconnect)
	if !viewer && !operator {
		return denied()
	}
	return safeError(s.core.CloseSession(ctx, id))
}

// DiagnoseResult preserves diagnostic failure through outcome-aware dispatch.
// Unknown checks are not passed checks, even when no operational failure is known.
type DiagnoseResult struct{ api.DiagnoseResponse }

func (r DiagnoseResult) CommandResultFailure() error {
	for _, stream := range r.Streams {
		for _, check := range stream.Checks {
			if check.State == api.CheckFailed {
				if check.Error != nil {
					return cs.NewError(check.Error.Code, nil)
				}
				return cs.NewError(cs.CodeSourceUnavailable, nil)
			}
		}
	}
	return nil
}
func (s *CameraService) Diagnose(ctx context.Context, id cs.CameraID, stream cs.StreamID) (DiagnoseResult, error) {
	ctx = s.captureCatalog(ctx)
	out := DiagnoseResult{api.DiagnoseResponse{CameraID: id, Streams: []api.StreamDiagnosis{}}}
	item, ok := s.snapshot(ctx).cameras[id]
	if !ok {
		return out, denied()
	}
	selected := []cs.Stream{}
	for _, profile := range item.Streams {
		if stream == "" || profile.ID == stream {
			selected = append(selected, profile)
		}
	}
	if len(selected) == 0 {
		return out, denied()
	}
	for _, profile := range selected {
		if _, err := s.require(ctx, id, profile.ID, diagnose); err != nil {
			return out, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	for _, profile := range selected {
		state, err := s.core.StreamStatus(ctx, item.Camera, profile.ID)
		out.Streams = append(out.Streams, api.StreamDiagnosis{StreamStatus: publicStatus(profile.ID, state, err), Capabilities: streamSummary(profile).Capabilities, Checks: diagnosticChecks(state, err)})
	}
	return out, nil
}
func diagnosticChecks(state cs.StreamState, err error) []api.DiagnosticCheck {
	checks := []api.DiagnosticCheck{{Name: "backend", State: api.CheckUnknown}, {Name: "source", State: api.CheckUnknown}, {Name: "media", State: api.CheckUnknown}}
	if err == nil && state.Fault != "" {
		err = cs.NewError(state.Fault, nil)
	}
	if err != nil {
		body := api.PublicError(err).Error
		index := 0
		if slices.Contains([]cs.ErrorCode{cs.CodeSourceUnavailable, cs.CodeAuthenticationFailed, cs.CodeCameraUnreachable, cs.CodeStreamUnavailable}, body.Code) {
			index = 1
		}
		if body.Code == cs.CodeMediaFailed || body.Code == cs.CodeSignalingFailed {
			index = 2
		}
		checks[index].State, checks[index].Error = api.CheckFailed, &body
		return checks
	}
	if state.Upstream == cs.PresencePresent && !state.ObservedAt.IsZero() {
		checks[0].State = api.CheckPassed
	}
	if state.State == cs.StatePlaying && state.Upstream == cs.PresencePresent {
		checks[1].State, checks[2].State = api.CheckPassed, api.CheckPassed
	}
	return checks
}

func (s *CameraService) Reconnect(ctx context.Context, id cs.CameraID, stream cs.StreamID) (api.ReconnectResponse, error) {
	ctx = s.captureCatalog(ctx)
	out := api.ReconnectResponse{Affected: []api.StreamRef{}, ClosedSessions: []cs.SessionID{}}
	item, err := s.require(ctx, id, stream, reconnect)
	if err != nil {
		return out, err
	}
	key := item.Sources[stream]
	// Reset affects every alias of the source. Partial operator grants cannot
	// interrupt or reveal another alias's viewers.
	for _, alias := range s.snapshot(ctx).aliases[key] {
		if _, err = s.require(ctx, alias.CameraID, alias.StreamID, reconnect); err != nil {
			return out, err
		}
	}
	out.Affected = append(out.Affected, s.snapshot(ctx).aliases[key]...)
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	ids, err := s.core.ResetSource(ctx, key)
	if cs.DescribeError(err).Code == cs.CodeNotFound {
		return out, nil
	} // no admitted source exists; no acquisition is triggered
	if err != nil {
		return out, safeError(err)
	}
	out.ClosedSessions = append(out.ClosedSessions, ids...)
	slices.Sort(out.ClosedSessions)
	return out, nil
}
func safeError(err error) error {
	if err == nil {
		return nil
	}
	// Preserve standard cancellation inspection without retaining raw causes.
	var cause error
	if errors.Is(err, context.Canceled) {
		cause = context.Canceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		cause = context.DeadlineExceeded
	}
	return cs.NewError(cs.DescribeError(err).Code, cause)
}
