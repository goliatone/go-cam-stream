package viewer

import (
	"context"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

func (s *CameraManagementService) resolveBaseBindings(ctx context.Context) ([]AddressBinding, error) {
	bindings := []AddressBinding{}
	seen := map[cs.PhysicalCameraID]bool{}
	for _, item := range s.options.Base.Cameras {
		if seen[item.Camera.PhysicalID] {
			continue
		}
		seen[item.Camera.PhysicalID] = true
		var host string
		var port uint16
		switch cfg := item.Camera.Config.(type) {
		case rtsp.Config:
			host, port = cfg.Host, cfg.Port
		case ec70.Config:
			host, port = cfg.Host, cfg.Port
		default:
			return nil, api.ManagementFailure(api.CodeIdentityUnverified)
		}
		binding, err := resolveBinding(ctx, s.options.Policy, s.options.Lookup, host, port)
		if err != nil {
			return nil, api.ManagementFailure(api.CodeIdentityUnverified)
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}
func (s *CameraManagementService) observeBaseIdentities(ctx context.Context) error {
	bindings, err := s.resolveBaseBindings(ctx)
	if err != nil {
		return err
	}
	seen := map[cs.PhysicalCameraID]bool{}
	i := 0
	for _, item := range s.options.Base.Cameras {
		if seen[item.Camera.PhysicalID] {
			continue
		}
		seen[item.Camera.PhysicalID] = true
		if err = s.identities.Observe(string(item.Camera.PhysicalID), bindings[i]); err != nil {
			return err
		}
		i++
	}
	return nil
}
func (s *CameraManagementService) CreateTest(ctx context.Context, id string, r api.CreateTestRequest) (api.TestResult, error) {
	v, err := s.operation(ctx, r.Mutation, api.OperationTest, id, r, func() (any, error) {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		d, err := s.ownedDraft(ctx, id)
		if err != nil {
			return nil, err
		}
		if d.review.Revision != r.ExpectedRevision {
			return nil, api.ManagementFailure(api.CodeRevisionConflict)
		}
		if d.review.State != api.DraftReady && d.review.State != api.DraftTested {
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		count, owned := 0, 0
		for _, other := range s.drafts {
			if other.review.State == api.DraftTesting || other.review.State == api.DraftCleaning || other.review.State == api.DraftCleanupFailed {
				count++
				if other.owner == d.owner {
					owned++
				}
			}
		}
		if count >= api.MaxTests || owned >= api.MaxOwnerTests {
			return nil, cs.NewError(cs.CodeCapacityExceeded, nil)
		}
		var record ManagedRecord
		if d.review.CredentialUpdate {
			record, err = s.credentialTarget(ctx, d)
			if err != nil {
				return nil, err
			}
		} else {
			if err = s.observeBaseIdentities(ctx); err != nil {
				return nil, err
			}
			if d.binding.Selected == "" {
				d.binding, err = resolveBinding(ctx, s.options.Policy, s.options.Lookup, d.config.Host, d.config.Port)
				if err != nil {
					return nil, err
				}
			}
			record = ManagedRecord{ID: cs.CameraID(d.review.CandidateID), PhysicalID: cs.PhysicalCameraID(d.review.CandidateID), Name: d.review.Name, Binding: d.binding, Path: d.config.Profiles[0].Path, Query: d.config.Profiles[0].Query}
		}
		if err = s.reserveDraft(ctx, d); err != nil {
			return nil, err
		}
		if d.review.CredentialUpdate {
			release, err := s.prepareCredentialUpdate(ctx, record)
			if err != nil {
				s.identities.Release(id)
				return nil, err
			}
			defer release()
		}
		if d.credential != nil {
			record.CredentialID = d.credential.ID
		}
		compiled, err := compileManaged(ctx, record)
		if err != nil {
			s.identities.Release(id)
			return nil, err
		}
		d.camera = compiled.Camera
		now := time.Now()
		d.passedAt = time.Time{}
		d.testOutcome = ""
		d.review.State = api.DraftTesting
		if !d.review.CredentialUpdate {
			d.review.DialAddress = d.binding.Selected
		}
		d.review.Test = &api.TestResult{ID: randomManagementID(), AttemptKey: r.OperationKey, DraftRevision: d.review.Revision, Stage: api.TestNegotiating, ExpiresAt: now.Add(api.TestDeadline), Cleanup: api.CleanupPending}
		testCtx, cancel := context.WithDeadline(ctx, d.review.Test.ExpiresAt)
		d.testCancel = cancel
		defer cancel()
		session, err := s.options.Core.CreateTransientSession(testCtx, d.camera, "default", cs.Offer{SDP: r.Offer.SDP})
		d.session = session.ID
		if err != nil {
			d.failTest(err)
			if cleanErr := s.cleanupDraft(d); cleanErr != nil {
				return pendingCleanup(d), cleanErr
			}
			return cloneTest(*d.review.Test), nil
		}
		if session.Answer == nil {
			d.failTest(cs.NewError(cs.CodeSignalingFailed, nil))
			if err = s.cleanupDraft(d); err != nil {
				return pendingCleanup(d), err
			}
			return cloneTest(*d.review.Test), nil
		}
		d.review.Test.Answer = &api.SDPAnswer{Type: "answer", SDP: session.Answer.SDP}
		d.review.Test.Stage = api.TestSourceMedia
		return cloneTest(*d.review.Test), nil
	})
	if err != nil {
		return api.TestResult{}, err
	}
	return cloneTest(v.(api.TestResult)), nil
}
func (s *CameraManagementService) currentTest(ctx context.Context, id, test string) (*cameraDraft, error) {
	d, err := s.ownedDraft(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.review.Test == nil || d.review.Test.ID != test {
		return nil, denied()
	}
	return d, nil
}
func (s *CameraManagementService) Test(ctx context.Context, id, test string) (api.TestResult, error) {
	if err := s.options.Policy.Require(ctx); err != nil {
		return api.TestResult{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return api.TestResult{}, err
	}
	defer s.release()
	d, err := s.currentTest(ctx, id, test)
	if err != nil {
		return api.TestResult{}, err
	}
	if d.review.State == api.DraftTesting {
		if time.Now().After(d.review.Test.ExpiresAt) {
			if err = s.cleanupDraft(d); err != nil {
				return api.TestResult{}, err
			}
		} else {
			state, observeErr := s.options.Core.StreamStatus(ctx, d.camera, "default")
			if observeErr != nil || state.Fault != "" {
				if observeErr == nil {
					observeErr = cs.NewError(state.Fault, nil)
				}
				d.failTest(observeErr)
				if err = s.cleanupDraft(d); err != nil {
					return api.TestResult{}, err
				}
				return cloneTest(*d.review.Test), nil
			}
			if observeErr == nil && state.State == cs.StatePlaying && state.Upstream == cs.PresencePresent && time.Since(state.ObservedAt) <= 5*time.Second && state.Fault == "" {
				d.review.Test.SourceMediaObserved = true
				d.review.Test.Stage = api.TestBrowserPlayback
			}
		}
	}
	return cloneTest(*d.review.Test), nil
}
func (s *CameraManagementService) TestByAttempt(ctx context.Context, id, key string) (api.TestResult, error) {
	if err := s.options.Policy.Require(ctx); err != nil {
		return api.TestResult{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return api.TestResult{}, err
	}
	d, err := s.ownedDraft(ctx, id)
	test := ""
	if err == nil && d.review.Test != nil && d.review.Test.AttemptKey == key {
		test = d.review.Test.ID
	}
	s.release()
	if err != nil {
		return api.TestResult{}, err
	}
	if test == "" {
		return api.TestResult{}, denied()
	}
	return s.Test(ctx, id, test)
}
func (s *CameraManagementService) CompleteTest(ctx context.Context, id, test string, r api.CompleteTestRequest) (api.TestResult, error) {
	v, err := s.operation(ctx, r.Mutation, api.OperationComplete, id, struct {
		Test    string
		Request api.CompleteTestRequest
	}{test, r}, func() (any, error) {
		d, err := s.currentTest(ctx, id, test)
		if err != nil {
			return nil, err
		}
		if d.review.Revision != r.ExpectedRevision {
			return nil, api.ManagementFailure(api.CodeRevisionConflict)
		}
		if d.review.State == api.DraftTested {
			return cloneTest(*d.review.Test), nil
		}
		if d.review.State != api.DraftTesting && d.review.State != api.DraftCleanupFailed {
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		if d.testOutcome == api.TestCanceled || d.testOutcome == api.TestFailed || d.cancelRequested {
			return nil, api.ManagementFailure(api.CodeTestExpired)
		}
		if err = r.Evidence.Validate(); err != nil {
			return nil, err
		}
		if d.passedAt.IsZero() {
			if time.Now().After(d.review.Test.ExpiresAt) {
				if err = s.cleanupDraft(d); err != nil {
					return nil, err
				}
				return nil, api.ManagementFailure(api.CodeTestExpired)
			}
			state, err := s.options.Core.StreamStatus(ctx, d.camera, "default")
			if err != nil || state.State != cs.StatePlaying || state.Upstream != cs.PresencePresent || state.Fault != "" || time.Since(state.ObservedAt) > 5*time.Second {
				return nil, api.ManagementFailure(api.CodeEvidenceInsufficient)
			}
			d.passedAt = time.Now()
			d.testOutcome = api.TestPassed
			d.review.Test.SourceMediaObserved = true
			d.review.Test.BrowserFramesReported = true
		}
		if err = s.cleanupDraft(d); err != nil {
			return pendingCleanup(d), err
		}
		return cloneTest(*d.review.Test), nil
	})
	if err != nil {
		return api.TestResult{}, err
	}
	return cloneTest(v.(api.TestResult)), nil
}
func (s *CameraManagementService) CancelTest(ctx context.Context, id, test string, r api.Mutation) (api.TestResult, error) {
	v, err := s.operation(ctx, r, api.OperationCancelTest, id, struct {
		Test     string
		Mutation api.Mutation
	}{test, r}, func() (any, error) {
		d, err := s.currentTest(ctx, id, test)
		if err != nil {
			return nil, err
		}
		if d.review.State == api.DraftSaved || d.review.State == api.DraftSaving {
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		d.cancelTest()
		if err = s.cleanupDraft(d); err != nil {
			return pendingCleanup(d), err
		}
		return cloneTest(*d.review.Test), nil
	})
	if err != nil {
		return api.TestResult{}, err
	}
	return cloneTest(v.(api.TestResult)), nil
}
func (d *cameraDraft) failTest(err error) {
	d.testOutcome = api.TestFailed
	d.passedAt = time.Time{}
	d.review.Test.PassedAt = nil
	body := api.ManagementPublicError(err).Error
	d.review.Test.Error = &body
}

func (d *cameraDraft) cancelTest() {
	d.testOutcome = api.TestCanceled
	d.passedAt = time.Time{}
	d.review.Test.PassedAt = nil
	d.review.Test.Error = nil
}

func (s *CameraManagementService) cleanupDraft(d *cameraDraft) error {
	if d.testCancel != nil {
		d.testCancel()
		d.testCancel = nil
	}
	if d.review.Test == nil {
		s.identities.Release(d.review.ID)
		return nil
	}
	// Record terminal intent before I/O; retries must preserve it independently
	// of the cleaning/cleanup_failed presentation stage.
	if d.cancelRequested || time.Now().After(d.review.ExpiresAt) {
		d.cancelTest()
	} else if d.testOutcome == "" && time.Now().After(d.review.Test.ExpiresAt) {
		d.failTest(api.ManagementFailure(api.CodeTestExpired))
	} else if d.testOutcome == api.TestPassed && time.Since(d.passedAt) > api.TestPassLifetime {
		d.failTest(api.ManagementFailure(api.CodeTestExpired))
	}
	d.review.State = api.DraftCleaning
	d.review.Test.Stage = api.TestCleaning
	ctx, cancel := context.WithTimeout(context.Background(), s.options.CleanupTimeout)
	defer cancel()
	var err error
	if d.session != "" {
		err = s.options.Core.CloseSession(ctx, d.session)
		if cs.DescribeError(err).Code == cs.CodeNotFound {
			err = nil
		}
	}
	if d.camera.Config != nil {
		// Retirement independently checks there are no live sessions. It can recover
		// a previous reset failure even when CloseSession still reports that latch.
		err = s.options.Core.RetireTransientSource(ctx, d.camera, "default")
	}
	if err != nil {
		d.review.State = api.DraftCleanupFailed
		d.review.Test.Stage = api.TestCleanupFailed
		d.review.Test.Cleanup = api.CleanupUnknown
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	d.session = ""
	d.review.Test.Cleanup = api.CleanupConfirmed
	if d.testOutcome == api.TestPassed && !d.passedAt.IsZero() {
		d.review.State = api.DraftTested
		d.review.Test.Stage = api.TestPassed
		when := d.passedAt
		d.review.Test.PassedAt = &when
	} else {
		d.passedAt = time.Time{}
		d.review.State = api.DraftReady
		d.review.Test.Stage = d.testOutcome
		d.review.Test.PassedAt = nil
		s.identities.Release(d.review.ID)
	}
	if d.cancelRequested || time.Now().After(d.review.ExpiresAt) {
		s.discardCredential(d)
		s.identities.Release(d.review.ID)
		d.passedAt = time.Time{}
		d.review.State = api.DraftCanceled
		if time.Now().After(d.review.ExpiresAt) {
			d.review.State = api.DraftExpired
		}
		d.review.Test.Stage = api.TestCanceled
		d.review.Test.PassedAt = nil
	}
	s.settleProbeOperations(d)
	return nil
}

func (s *CameraManagementService) settleProbeOperations(d *cameraDraft) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	for _, op := range s.operations {
		pending := op.recovery
		if op.target != d.review.ID || op.owner != d.owner || op.status.State != api.OperationUnknown || pending == nil || pending.testID != d.review.Test.ID || pending.revision != d.review.Revision {
			continue
		}
		switch op.status.Kind {
		case api.OperationComplete:
			var err error
			if d.review.State != api.DraftTested {
				err = api.ManagementFailure(api.CodeTestExpired)
			}
			settleManagementOperation(op, cloneTest(*d.review.Test), err)
		case api.OperationTest, api.OperationCancelTest:
			settleManagementOperation(op, cloneTest(*d.review.Test), nil)
		case api.OperationCancel:
			settleManagementOperation(op, cloneReview(d.review), nil)
		}
	}
}
