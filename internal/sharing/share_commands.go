package sharing

import "github.com/goliatone/go-cam-stream/data/api"

type CreateShare struct {
	api.CreateShareRequest
}

func (CreateShare) Type() string { return api.ShareCreateCommand }

type CopyShareLink struct{ api.ShareRef }

func (CopyShareLink) Type() string { return api.ShareCopyCommand }

type RevokeShare struct{ api.ShareRef }

func (RevokeShare) Type() string { return api.ShareRevokeCommand }

type RedeemShare struct {
	api.ShareRef
	api.RedeemShareRequest
}

func (RedeemShare) Type() string { return api.ShareRedeemCommand }

func (m RedeemShare) Validate() error {
	if err := m.ShareRef.Validate(); err != nil {
		return err
	}
	return m.RedeemShareRequest.Validate()
}

type AdmitShareViewer struct {
	api.ShareRef
	api.AdmitShareViewerRequest
}

func (AdmitShareViewer) Type() string { return api.ShareAdmitViewerCommand }

func (m AdmitShareViewer) Validate() error {
	if err := m.ShareRef.Validate(); err != nil {
		return err
	}
	return m.AdmitShareViewerRequest.Validate()
}

// ShareViewerRef is populated from the validated route, independently of
// the untrusted JSON body. Its IDs are selectors, with no authority implied.
type ShareViewerRef struct {
	api.ShareRef
	ViewerID string `json:"viewer_id"`
}

func (r ShareViewerRef) Validate() error {
	if !api.ValidShareID(r.ViewerID) {
		return api.ErrInvalidSharingRequest
	}
	return r.ShareRef.Validate()
}

type OpenShareViewer struct {
	ShareViewerRef
	api.OpenShareViewerRequest
}

func (OpenShareViewer) Type() string { return api.ShareOpenViewerCommand }

func (m OpenShareViewer) Validate() error {
	if err := m.ShareViewerRef.Validate(); err != nil {
		return err
	}
	return m.OpenShareViewerRequest.Validate()
}

type CloseShareViewer struct {
	ShareViewerRef
	api.ShareViewerAttempt
}

func (CloseShareViewer) Type() string { return api.ShareCloseViewerCommand }

func (m CloseShareViewer) Validate() error {
	if err := m.ShareViewerRef.Validate(); err != nil {
		return err
	}
	return m.ShareViewerAttempt.Validate()
}
