package api

import (
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"regexp"
)

var operationKeyPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (m Mutation) Validate() error {
	if !operationKeyPattern.MatchString(m.OperationKey) || m.RuntimeEpoch == "" || len(m.RuntimeEpoch) > 128 {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}
func (r CreateTestRequest) Validate() error {
	if err := r.Mutation.Validate(); err != nil {
		return err
	}
	if r.ExpectedRevision == 0 {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return (CreateSessionRequest{Offer: r.Offer}).Validate()
}

// Validate checks shape only; the service applies the draft name rule.
func (r RenameCameraRequest) Validate() error {
	if err := r.Mutation.Validate(); err != nil {
		return err
	}
	if r.Name == "" || r.CatalogRevision.RuntimeEpoch == "" {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}
func (r RemoveCameraRequest) Validate() error {
	if err := r.Mutation.Validate(); err != nil {
		return err
	}
	if r.CatalogRevision.RuntimeEpoch == "" {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}
func (r StopViewersRequest) Validate() error {
	if err := r.Mutation.Validate(); err != nil {
		return err
	}
	if r.ExpectedRevision == 0 {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}
func (r FrameEvidence) Validate() error {
	if r.Method != "video_frame_callback" && r.Method != "frames_decoded" && r.Method != "playback_quality" {
		return ManagementFailure(CodeEvidenceUnavailable)
	}
	if r.Frames < 2 || r.Width == 0 || r.Height == 0 || r.Paused {
		return ManagementFailure(CodeEvidenceInsufficient)
	}
	return nil
}
