package go2rtc

import (
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

func (b *Backend) register(ctx context.Context, spec camstream.SessionSpec) (*session, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running || b.runtime.Err() != nil {
		return nil, false, failure(camstream.CodeBackendUnavailable, nil)
	}
	if v := b.sessions[spec.ID]; v != nil {
		if v.aborted || v.spec.SourceKey != spec.SourceKey || v.offerHash != sha256.Sum256([]byte(spec.Offer.SDP)) {
			return nil, false, failure(camstream.CodeConflict, nil)
		}
		return v, false, nil
	}
	s := b.sources[spec.SourceKey]
	if s == nil {
		return nil, false, failure(camstream.CodeNotFound, nil)
	}
	if b.active >= b.cfg.MaxSessions || len(b.sessions) >= b.cfg.MaxSessionRecords {
		return nil, false, failure(camstream.CodeCapacityExceeded, nil)
	}
	op, cancel := context.WithCancel(context.Background())
	v := &session{offerHash: sha256.Sum256([]byte(spec.Offer.SDP)), source: s, spec: spec, cancel: cancel, operation: op, state: camstream.SessionState{ID: spec.ID, SourceKey: spec.SourceKey, State: camstream.StateConnecting, Resources: camstream.PresenceAbsent}}
	b.sessions[spec.ID] = v
	b.active++
	return v, true, nil
}
func (b *Backend) CreateSession(ctx context.Context, spec camstream.SessionSpec) (camstream.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return camstream.SessionState{}, err
	}
	if spec.ID == "" || spec.SourceKey == "" || len(spec.Offer.SDP) > 64<<10 || !strings.HasPrefix(spec.Offer.SDP, "v=0\r\n") {
		return camstream.SessionState{}, failure(camstream.CodeInvalidArgument, nil)
	}
	if err := validateOffer(spec.Offer); err != nil {
		return camstream.SessionState{}, err
	}
	v, fresh, err := b.register(ctx, spec)
	if err != nil {
		return camstream.SessionState{}, err
	}
	s := v.source
	waitCtx := ctx
	if fresh {
		waitCtx = v.operation
		stopRequest := context.AfterFunc(ctx, v.cancel)
		defer stopRequest()
		stopRuntime := context.AfterFunc(b.runtime, v.cancel)
		defer stopRuntime()
	}
	if err = enter(waitCtx, s); err != nil {
		if fresh {
			v.cancel()
			b.mu.Lock()
			v.aborted = true
			b.active--
			b.mu.Unlock()
		}
		return camstream.SessionState{}, err
	}
	defer leave(s)
	if !fresh {
		return v.negotiatedResult()
	}
	defer func() { v.spec.Offer.SDP = "" }() // bounded tombstones retain a digest, not SDP
	s.sessions[spec.ID] = v
	defer v.cancel()
	if code := b.admissionFault(s); code != "" {
		b.finishSession(s, v, code)
		return v.state, failure(code, nil)
	}
	return b.createLocked(v)
}
func (v *session) negotiatedResult() (camstream.SessionState, error) {
	if v.answer != nil && v.state.Resources != camstream.PresenceAbsent {
		out := v.state
		answer := *v.answer
		out.Answer = &answer
		return out, nil
	}
	return v.state, failure(camstream.CodeConflict, nil)
}
func (b *Backend) createLocked(v *session) (camstream.SessionState, error) {
	err := b.negotiate(v)
	if err == nil {
		err = v.operation.Err()
	}
	if err != nil {
		cleanup, cancel := b.cleanupContext()
		defer cancel()
		if closeErr := b.closeSession(cleanup, v.source, v, camstream.DescribeError(err).Code); closeErr != nil {
			return v.state, closeErr
		}
		return v.state, err
	}
	return v.negotiatedResult()
}

func (b *Backend) negotiate(v *session) error {
	s := v.source
	ctx, cancel := context.WithTimeout(v.operation, b.cfg.StartupTimeout)
	defer cancel()
	if s.ingest == nil {
		var err error
		s.ingest, err = b.launch(ctx, true, s.input)
		if err != nil {
			return err
		}
		s.lastProgress = time.Now()
		s.received = 0
	}
	if s.ingest != nil {
		check, finish := context.WithTimeout(ctx, b.cfg.PollInterval)
		obs, observeErr := b.observe(check, s.ingest)
		finish()
		if observeErr == nil && obs.received() > s.received {
			s.received = obs.received()
			s.lastProgress = time.Now()
		}
	}
	var err error
	v.worker, err = b.launch(ctx, false, relay(s.ingest))
	if v.worker != nil {
		v.state.Resources = camstream.PresencePresent
	}
	if err != nil {
		return err
	}
	negotiation, done := context.WithDeadline(ctx, s.lastProgress.Add(b.cfg.FailureTimeout))
	defer done()
	body, err := b.request(negotiation, v.worker, "POST", "/api/webrtc?src=stream", []byte(v.spec.Offer.SDP))
	if err != nil {
		if v.operation.Err() != nil {
			return v.operation.Err()
		}
		// A stalled/denied source has no trustworthy auth classification. Preserve
		// the source latch even when the signaling request itself merely times out.
		check, finish := context.WithTimeout(context.Background(), b.cfg.PollInterval)
		defer finish()
		obs, observeErr := b.observe(check, s.ingest)
		if observeErr != nil || obs.received() == 0 {
			s.fault = camstream.CodeSourceUnavailable
			return failure(s.fault, nil)
		}
		return failure(camstream.CodeSignalingFailed, err)
	}
	if !strings.HasPrefix(string(body), "v=0\r\n") {
		return failure(camstream.CodeSignalingFailed, nil)
	}
	v.answer = &camstream.Answer{SDP: string(body)}
	v.lastProgress = time.Now()
	v.state.ObservedAt = time.Now()
	return nil
}
func (b *Backend) SessionStatus(ctx context.Context, id camstream.SessionID) (camstream.SessionState, error) {
	b.mu.Lock()
	v := b.sessions[id]
	aborted := v != nil && v.aborted
	b.mu.Unlock()
	if v == nil {
		return camstream.SessionState{}, failure(camstream.CodeNotFound, nil)
	}
	if aborted {
		return camstream.SessionState{ID: id, SourceKey: v.spec.SourceKey, State: camstream.StateFailed, Resources: camstream.PresenceAbsent, Fault: camstream.CodeCanceled}, nil
	}
	if err := enter(ctx, v.source); err != nil {
		return camstream.SessionState{}, err
	}
	defer leave(v.source)
	return v.state, nil
}
func (b *Backend) CloseSession(ctx context.Context, id camstream.SessionID) error {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CleanupTimeout)
	defer cancel()
	b.mu.Lock()
	v := b.sessions[id]
	aborted := v != nil && v.aborted
	b.mu.Unlock()
	if v == nil || aborted {
		return nil
	}
	v.cancel()
	if err := enter(ctx, v.source); err != nil {
		return failure(camstream.CodeCleanupIncomplete, err)
	}
	defer leave(v.source)
	return b.closeSession(ctx, v.source, v, "")
}
func (b *Backend) closeSession(ctx context.Context, s *source, v *session, code camstream.ErrorCode) error {
	v.state.State = camstream.StateClosing
	if err := b.stop(ctx, v.worker); err != nil {
		v.state.Resources = camstream.PresenceUnknown
		v.state.Fault = camstream.CodeCleanupIncomplete
		s.unknown = true
		return err
	}
	v.worker = nil
	b.finishSession(s, v, code)
	if len(s.sessions) == 0 && s.ingest != nil {
		if err := b.stop(ctx, s.ingest); err != nil {
			s.unknown = true
			return err
		}
		s.ingest = nil
		s.received = 0
		s.unknown = false
		s.observed = time.Now()
	}
	return nil
}
func (b *Backend) finishSession(s *source, v *session, code camstream.ErrorCode) {
	if current := s.sessions[v.spec.ID]; current == v {
		delete(s.sessions, v.spec.ID)
		b.mu.Lock()
		b.active--
		b.mu.Unlock()
	}
	v.answer = nil
	v.state.Resources = camstream.PresenceAbsent
	v.state.State = camstream.StateClosed
	v.state.Fault = code
	v.state.ObservedAt = time.Now()
	if code != "" {
		v.state.State = camstream.StateFailed
	}
}
func (b *Backend) cleanSource(ctx context.Context, s *source) error {
	var result error
	for _, v := range s.sessions {
		v.cancel()
		if err := b.closeSession(ctx, s, v, s.fault); err != nil {
			result = err
		}
	}
	if result != nil {
		return result
	}
	if s.ingest != nil {
		if err := b.stop(ctx, s.ingest); err != nil {
			s.unknown = true
			return err
		}
		s.ingest = nil
	}
	s.received = 0
	s.unknown = false
	s.observed = time.Now()
	return nil
}

func (b *Backend) admissionFault(s *source) camstream.ErrorCode {
	if s.removed || !b.isRunning() {
		return camstream.CodeBackendUnavailable
	}
	if s.ingest != nil && s.ingest.dead() {
		s.fault = camstream.CodeSourceUnavailable
	}
	if s.fault != "" {
		return s.fault
	}
	if s.unknown {
		return camstream.CodeCleanupIncomplete
	}
	for _, v := range s.sessions {
		if v.state.Fault == camstream.CodeCleanupIncomplete {
			return camstream.CodeCleanupIncomplete
		}
	}
	return ""
}
