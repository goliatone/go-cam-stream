package viewer

import (
	"bytes"
	"context"
	"slices"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

// CredentialDraft opens a settings draft for a saved camera: a new sign-in, a
// new address, or both. An empty username keeps the saved one, and an empty
// password keeps it only when the address changes. An anonymous camera given
// a new address and no sign-in stays anonymous.
func (s *CameraManagementService) CredentialDraft(ctx context.Context, r api.CredentialDraftRequest) (api.DraftReview, error) {
	value, err := s.operation(ctx, r.Mutation, api.OperationCreate, "", r, func() (any, error) {
		id := cs.CameraID(r.CameraID)
		i := slices.IndexFunc(s.durable.Cameras, func(c ManagedRecord) bool { return c.ID == id })
		if i < 0 || !s.options.Viewer.canUpdateCredentials(ctx, id) {
			return nil, denied()
		}
		record := s.durable.Cameras[i]
		var cfg rtsp.Config
		endpoint := ""
		if r.Endpoint != "" {
			input, normalized, err := normalizeDraft(api.DraftInput{Name: record.Name, Endpoint: r.Endpoint, Authentication: api.AuthenticationAnonymous})
			if err != nil {
				return nil, err
			}
			if input.Endpoint != endpointString(record.Binding.Host, record.Binding.Port, record.Path, record.Query) {
				cfg, endpoint = normalized, input.Endpoint
			}
		}
		anonymous := record.CredentialID == ""
		blank := r.Credentials.Username == "" && r.Credentials.Password == ""
		d, err := s.newDraft(ctx, record.Name)
		if err != nil {
			return nil, err
		}
		d.review.CandidateID = r.CameraID
		d.review.CredentialUpdate = true
		d.review.Authentication = api.AuthenticationCredentials
		d.priorCredential = record.CredentialID
		d.connectionRevision = record.ConnectionRevision
		d.binding = record.Binding
		if endpoint != "" {
			d.addressChange, d.config, d.binding = true, cfg, AddressBinding{}
			d.review.Endpoint = endpoint
			if cfg.Host == "localhost" || cfg.Host == "127.0.0.1" || cfg.Host == "::1" {
				d.review.Warnings = append(d.review.Warnings, api.WarningServerLocalhost)
			}
		}
		switch {
		case anonymous && endpoint != "" && blank:
			d.review.Authentication = api.AuthenticationAnonymous
		case !anonymous && endpoint != "" && r.Credentials.Password == "":
			err = s.copySavedCredential(d, r.Credentials.Username)
		default:
			err = s.replaceDraftPassword(d, r.Credentials)
		}
		if err != nil {
			return nil, err
		}
		s.drafts[d.review.ID] = d
		return cloneReview(d.review), nil
	})
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(value.(api.DraftReview)), nil
}

func (s *CameraManagementService) replaceDraftPassword(d *cameraDraft, c api.Credentials) error {
	if c.Username == "" && d.priorCredential != "" {
		for _, saved := range s.durable.Credentials {
			if saved.ID == d.priorCredential {
				c.Username = string(saved.Username)
				break
			}
		}
	}
	if !validManagedCredential(ManagedCredential{Username: []byte(c.Username), Password: []byte(c.Password)}) {
		return api.ManagementFailure(api.CodeCredentialsRequired)
	}
	return s.replaceCredentials(d, api.DraftInput{Authentication: api.AuthenticationCredentials, Credentials: &c})
}

// copySavedCredential gives an address change its own copy of the saved
// sign-in, so saving replaces the saved credential rather than sharing it.
func (s *CameraManagementService) copySavedCredential(d *cameraDraft, username string) error {
	for _, saved := range s.durable.Credentials {
		if saved.ID != d.priorCredential {
			continue
		}
		c := ManagedCredential{ID: randomManagementID(), Username: []byte(username), Password: bytes.Clone(saved.Password)}
		if username == "" {
			c.Username = bytes.Clone(saved.Username)
		}
		if !validManagedCredential(c) {
			clear(c.Username)
			clear(c.Password)
			return api.ManagementFailure(api.CodeCredentialsRequired)
		}
		if err := s.options.Secrets.PutDraft(c); err != nil {
			clear(c.Username)
			clear(c.Password)
			return err
		}
		s.discardCredential(d)
		d.credential = &c
		d.review.CredentialsPresent = true
		return nil
	}
	return api.ManagementFailure(api.CodeCredentialsChanged)
}

func (s *CameraManagementService) credentialTarget(ctx context.Context, d *cameraDraft) (ManagedRecord, error) {
	id := cs.CameraID(d.review.CandidateID)
	if !s.options.Viewer.canUpdateCredentials(ctx, id) {
		return ManagedRecord{}, denied()
	}
	for _, record := range s.durable.Cameras {
		if record.ID == id && record.ConnectionRevision == d.connectionRevision {
			return record, nil
		}
	}
	return ManagedRecord{}, api.ManagementFailure(api.CodeCredentialsChanged)
}

func (s *CameraManagementService) reserveDraft(ctx context.Context, d *cameraDraft) error {
	var existing string
	if d.review.CredentialUpdate {
		existing = d.review.CandidateID
	}
	owner, err := s.identities.reserve(d.review.ID, existing, d.binding)
	if owner != "" && s.options.Viewer.canUpdateCredentials(ctx, cs.CameraID(owner)) {
		return api.DuplicateManagedCamera(owner)
	}
	return err
}

// Lock admission while releasing an inactive source's backend resources.
// The caller releases the fence after testing or after resolving the durable
// save. A draft that already holds the fence keeps it; its release is a no-op.
func (s *CameraManagementService) prepareCredentialUpdate(ctx context.Context, d *cameraDraft, record ManagedRecord) (func(), cs.SourceKey, error) {
	camera, err := compileManaged(ctx, record)
	if err != nil {
		return nil, "", err
	}
	release := func() {}
	if d.fence == nil {
		if release, err = s.options.Viewer.beginSourceChange(record.ID); err != nil {
			return nil, "", err
		}
	}
	if err = s.options.Core.PrepareInactiveSource(ctx, camera.Camera, "default"); err != nil {
		release()
		return nil, "", err
	}
	return release, camera.Sources["default"], nil
}

// StopViewers closes every session of a settings draft's camera and keeps new
// ones out until the draft is saved, canceled or expires, or the host stops.
// Its test and save reuse the fence instead of waiting for viewers to leave.
func (s *CameraManagementService) StopViewers(ctx context.Context, id string, r api.StopViewersRequest) (api.DraftReview, error) {
	v, err := s.operation(ctx, r.Mutation, api.OperationStopViewers, id, r, func() (any, error) {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		d, err := s.ownedDraft(ctx, id)
		if err != nil {
			return nil, err
		}
		if !d.review.CredentialUpdate {
			return nil, invalid()
		}
		if d.review.Revision != r.ExpectedRevision {
			return nil, api.ManagementFailure(api.CodeRevisionConflict)
		}
		if d.review.State != api.DraftReady && d.review.State != api.DraftIncomplete && d.review.State != api.DraftTested {
			return nil, api.ManagementFailure(api.CodeOperationInProgress)
		}
		record, err := s.credentialTarget(ctx, d)
		if err != nil {
			return nil, err
		}
		camera, err := compileManaged(ctx, record)
		if err != nil {
			return nil, err
		}
		taken := d.fence == nil
		if taken {
			release, err := s.options.Viewer.beginSourceChange(record.ID)
			if err != nil {
				return nil, err
			}
			d.fence = release
		}
		if _, err = s.options.Core.ResetSource(ctx, camera.Sources["default"]); err != nil && cs.DescribeError(err).Code != cs.CodeNotFound {
			if taken {
				s.releaseFence(d)
			}
			return nil, safeError(err)
		}
		d.review.Fenced = true
		return cloneReview(d.review), nil
	})
	if err != nil {
		return api.DraftReview{}, err
	}
	return cloneReview(v.(api.DraftReview)), nil
}

func (s *CameraManagementService) releaseFence(d *cameraDraft) {
	if d.fence != nil {
		d.fence()
		d.fence = nil
	}
	d.review.Fenced = false
}
