package api

import (
	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"strings"
)

const MaxSDPBytes = 64 * 1024

// Validate checks the negotiation envelope. The backend's SDP parser owns
// protocol validation; this is not evidence of usable ICE or media.
func (r CreateSessionRequest) Validate() error {
	if r.Offer.Type != "offer" || len(r.Offer.SDP) > MaxSDPBytes || !strings.HasPrefix(r.Offer.SDP, "v=0\r\n") {
		return camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	return nil
}
