package go2rtc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

func (b *Backend) request(ctx context.Context, w *worker, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, w.endpoint()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/sdp")
	}
	res, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close() //nolint:errcheck // Response is bounded/read below; Close cannot alter the observed result.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, failure(camstream.CodeBackendUnavailable, nil)
	}
	out, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(out) > 1<<20 {
		return nil, failure(camstream.CodeBackendUnavailable, nil)
	}
	return out, nil
}

type connection struct {
	ID       uint64 `json:"id"`
	Format   string `json:"format_name"`
	Received uint64 `json:"bytes_recv"`
	Sent     uint64 `json:"bytes_send"`
}
type observation struct {
	Producers []connection `json:"producers"`
	Consumers []connection `json:"consumers"`
}

func (o observation) received() uint64 {
	var n uint64
	for _, p := range o.Producers {
		n += p.Received
	}
	return n
}
func (o observation) viewer() (uint64, bool) {
	for _, c := range o.Consumers {
		if (c.Format == "webrtc" || c.Format == "webrtc/whep") && c.ID != 0 {
			return c.Sent, true
		}
	}
	return 0, false
}
func (b *Backend) observe(ctx context.Context, w *worker) (observation, error) {
	var out observation
	if w.dead() || b.listenersOwned(w) != nil {
		return out, failure(camstream.CodeBackendUnavailable, nil)
	}
	body, err := b.request(ctx, w, http.MethodGet, "/api/streams?src=stream", nil)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(body, &out)
	return out, err
}
