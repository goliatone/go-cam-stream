package viewer

import (
	"context"
	"slices"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func (s *CameraManagementService) CredentialDraft(ctx context.Context, r api.CredentialDraftRequest) (api.DraftReview, error) {
	value, err := s.operation(ctx, r.Mutation, api.OperationCreate, "", r, func() (any, error) {
		id := cs.CameraID(r.CameraID)
		i := slices.IndexFunc(s.durable.Cameras, func(c ManagedRecord) bool { return c.ID == id })
		if i < 0 || !s.options.Viewer.canUpdateCredentials(ctx, id) {
			return nil, denied()
		}
		record := s.durable.Cameras[i]
		d, err := s.newDraft(ctx, record.Name)
		if err != nil {
			return nil, err
		}
		d.review.CandidateID = r.CameraID
		d.review.CredentialUpdate = true
		d.review.Authentication = api.AuthenticationCredentials
		d.priorCredential = record.CredentialID
		d.binding = record.Binding
		if err = s.replaceDraftPassword(d, r.Credentials); err != nil {
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

func (s *CameraManagementService) credentialTarget(ctx context.Context, d *cameraDraft) (ManagedRecord, error) {
	id := cs.CameraID(d.review.CandidateID)
	if !s.options.Viewer.canUpdateCredentials(ctx, id) {
		return ManagedRecord{}, denied()
	}
	for _, record := range s.durable.Cameras {
		if record.ID == id && record.CredentialID == d.priorCredential {
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
// The caller releases the fence after testing or after resolving the durable save.
func (s *CameraManagementService) prepareCredentialUpdate(ctx context.Context, record ManagedRecord) (func(), cs.SourceKey, error) {
	camera, err := compileManaged(ctx, record)
	if err != nil {
		return nil, "", err
	}
	release, err := s.options.Viewer.beginCredentialUpdate(record.ID)
	if err != nil {
		return nil, "", err
	}
	if err = s.options.Core.PrepareInactiveSource(ctx, camera.Camera, "default"); err != nil {
		release()
		return nil, "", err
	}
	return release, camera.Sources["default"], nil
}
