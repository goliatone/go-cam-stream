package sharing

import (
	"context"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/pion/webrtc/v4"
)

// TestRelay is the explicit local operator probe. Its grant is short lived and
// scoped to the probe, and the probe never opens camera media.
func TestRelay(ctx context.Context, issuer RelayIssuer) error {
	if issuer == nil {
		return fault(api.ShareRelayUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	// Leave room for the provider's expiry margin and minimum credential TTL.
	grant := RelayGrant{Attempt: ViewerAttempt{ShareID: handle(), ViewerID: handle(), AttemptID: handle()}, NotAfter: time.Now().Add(2 * time.Minute)}
	issued, err := issuer.Issue(ctx, grant)
	if err != nil {
		return err
	}
	host, _, err := issued.ForAttempt(grant, time.Now())
	if err != nil {
		return err
	}
	ice, err := (MediaOffer{Attempt: grant.Attempt, Relay: &host}).HostICE(time.Now())
	if err != nil {
		return err
	}
	var servers []webrtc.ICEServer
	for _, server := range ice {
		servers = append(servers, webrtc.ICEServer{URLs: server.URLs, Username: server.Username, Credential: server.Credential})
	}
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{ICETransportPolicy: webrtc.ICETransportPolicyRelay, ICEServers: servers})
	if err != nil {
		return fault(api.ShareRelayUnavailable)
	}
	defer peer.Close() //nolint:errcheck // No application media is open.
	if _, err = peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return fault(api.ShareRelayUnavailable)
	}
	offer, err := peer.CreateOffer(nil)
	gathered := webrtc.GatheringCompletePromise(peer)
	if err != nil || peer.SetLocalDescription(offer) != nil {
		return fault(api.ShareRelayUnavailable)
	}
	select {
	case <-ctx.Done():
		return fault(api.ShareRelayUnavailable)
	case <-gathered:
	}
	if description := peer.LocalDescription(); description != nil && strings.Contains(description.SDP, " typ relay") {
		return nil
	}
	return fault(api.ShareRelayUnavailable)
}
