package sharing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/pion/sdp/v3"
)

var privatePeerID = regexp.MustCompile(`^[0-9a-f-]{36}$`)

func videoSDP(raw, direction string) bool {
	var session sdp.SessionDescription
	if len(raw) > api.ShareOfferMax || session.Unmarshal([]byte(raw)) != nil || len(session.MediaDescriptions) != 1 {
		return false
	}
	media := session.MediaDescriptions[0]
	if media.MediaName.Media != "video" || media.MediaName.Port.Value == 0 {
		return false
	}
	found := false
	for _, attr := range media.Attributes {
		switch attr.Key {
		case "sendrecv", "sendonly", "recvonly", "inactive":
			if found || attr.Key != direction {
				return false
			}
			found = true
		}
	}
	return found
}

func (m *MediaRuntime) Open(ctx context.Context, o MediaOffer) (MediaPeer, error) {
	if o.Offer.Type != "offer" || !videoSDP(o.Offer.SDP, "recvonly") {
		return MediaPeer{}, fault(api.ShareUnsupportedOffer)
	}
	ice, err := o.HostICE(time.Now())
	if err != nil {
		return MediaPeer{}, fault(api.ShareRelayUnavailable)
	}
	peer, err := m.newPeer(ctx, o, ice)
	if err != nil {
		return MediaPeer{}, fault(api.ShareMediaUnreachable)
	}
	success := false
	defer func() {
		if !success {
			m.stop(peer)
		}
	}()
	response, err := m.fetch(ctx, peer, http.MethodPost, "http://"+peer.address+"/video/whep", o.Offer.SDP)
	if err != nil {
		return MediaPeer{}, fault(api.ShareMediaUnreachable)
	}
	defer response.Body.Close() //nolint:errcheck // Connection is private and bounded.
	id, resource := response.Header.Get("id"), response.Header.Get("Location")
	if response.StatusCode != http.StatusCreated || !privatePeerID.MatchString(id) ||
		!strings.HasPrefix(resource, "/video/whep/") || !privatePeerID.MatchString(strings.TrimPrefix(resource, "/video/whep/")) {
		return MediaPeer{}, fault(api.ShareMediaUnreachable)
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, api.ShareOfferMax+1))
	if err != nil || !videoSDP(string(answer), "sendonly") {
		return MediaPeer{}, fault(api.ShareMediaUnreachable)
	}
	peer.resource = "http://" + peer.address + resource
	control := "http://" + peer.id + "/v3/webrtcsessions/get/" + id
	m.mu.Lock()
	if m.peers[o.Attempt.AttemptID] != nil || ctx.Err() != nil || m.ctx.Err() != nil {
		m.mu.Unlock()
		return MediaPeer{}, fault(api.ShareMediaUnreachable)
	}
	m.peers[o.Attempt.AttemptID] = peer
	m.mu.Unlock()
	success = true
	go m.observe(peer, control)
	return MediaPeer{ID: o.Attempt.AttemptID, Answer: api.SDPAnswer{Type: "answer", SDP: string(answer)}}, nil
}

func (m *MediaRuntime) fetch(ctx context.Context, p *runtimePeer, method, target, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("gateway", p.token)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/sdp")
	}
	return m.client.Do(req)
}

func (m *MediaRuntime) waitReady(ctx context.Context, p *runtimePeer) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		response, err := m.fetch(ctx, p, http.MethodGet, "http://"+p.id+"/v3/webrtcsessions/list", "")
		if err == nil {
			if err = response.Body.Close(); err != nil {
				return err
			}
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.worker.done:
			return fault(api.ShareMediaUnreachable)
		case <-timer.C:
		}
	}
}

func (m *MediaRuntime) observe(peer *runtimePeer, control string) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var lastPackets uint64
	var lastMedia time.Time
	for {
		select {
		case <-peer.worker.done:
			if m.opts.OnLost != nil {
				m.opts.OnLost(peer.scope)
			}
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(m.ctx, 200*time.Millisecond)
		packets, ok := m.packets(ctx, peer, control)
		cancel()
		if !ok || (!lastMedia.IsZero() && time.Since(lastMedia) > 2*time.Second) {
			if m.opts.OnLost != nil {
				m.opts.OnLost(peer.scope)
			}
			return
		}
		if packets > lastPackets {
			if lastMedia.IsZero() && m.opts.OnLive != nil {
				m.opts.OnLive(peer.scope)
			}
			lastPackets, lastMedia = packets, time.Now()
		}
	}
}

func (m *MediaRuntime) packets(ctx context.Context, p *runtimePeer, control string) (uint64, bool) {
	res, err := m.fetch(ctx, p, http.MethodGet, control, "")
	if err != nil {
		return 0, false
	}
	defer res.Body.Close() //nolint:errcheck // Bounded private control request.
	var stats struct {
		OutboundRTPPackets uint64 `json:"outboundRTPPackets"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 16*1024)).Decode(&stats) != nil {
		return 0, false
	}
	return stats.OutboundRTPPackets, true
}

func (m *MediaRuntime) stop(peer *runtimePeer) {
	peer.worker.stop()
	if peer.dir != "" {
		_ = os.RemoveAll(peer.dir)
	}
	m.release(peer.scope.ShareID, peer.source)
}

func (m *MediaRuntime) Close(ctx context.Context, id string) error {
	m.mu.Lock()
	peer := m.peers[id]
	delete(m.peers, id)
	m.mu.Unlock()
	if peer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if response, err := m.fetch(ctx, peer, http.MethodDelete, peer.resource, ""); err == nil {
		_ = response.Body.Close()
	}
	// The attempt owns this entire worker. Reaping it closes media even after a
	// failed WHEP DELETE and leaves other viewers and the RTSP source alone.
	m.stop(peer)
	return nil
}
