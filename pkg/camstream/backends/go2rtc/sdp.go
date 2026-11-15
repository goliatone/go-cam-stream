package go2rtc

import (
	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/pion/sdp/v3"
)

// Parse signaling before allocating workers. This uses only the SDP parser, not
// a second PeerConnection/media stack. go2rtc still negotiates codecs and ICE.
func validateOffer(offer camstream.Offer) error {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(offer.SDP)); err != nil {
		return failure(camstream.CodeInvalidArgument, nil)
	}
	media := false
	for _, m := range description.MediaDescriptions {
		if m.MediaName.Media != "video" && m.MediaName.Media != "audio" {
			continue
		}
		media = true
		if _, ok := m.Attribute("recvonly"); !ok {
			return failure(camstream.CodeInvalidArgument, nil)
		}
	}
	if !media {
		return failure(camstream.CodeInvalidArgument, nil)
	}
	return nil
}
