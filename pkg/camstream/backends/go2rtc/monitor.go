package go2rtc

import (
	"context"
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

func (b *Backend) monitor() {
	defer close(b.monitorDone)
	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.runtime.Done():
			return
		case <-ticker.C:
		}
		b.mu.Lock()
		sources := make([]*source, 0, len(b.sources))
		for _, s := range b.sources {
			sources = append(sources, s)
		}
		b.mu.Unlock()
		var wg sync.WaitGroup
		for _, s := range sources {
			select {
			case s.gate <- struct{}{}:
			default:
				continue
			}
			wg.Go(func() { ; defer leave(s); b.poll(s) })
		}
		wg.Wait()
	}
}
func (b *Backend) poll(s *source) {
	if s.ingest == nil {
		return
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(b.runtime, b.cfg.PollInterval)
	defer cancel()
	obs, err := b.observe(ctx, s.ingest)
	if err != nil {
		s.unknown = true
	} else {
		s.observed = now
		s.unknown = false
		if n := obs.received(); n > s.received {
			s.lastProgress = now
			s.received = n
		}
	}
	if s.fault != "" || s.ingest.dead() || now.Sub(s.lastProgress) >= b.cfg.FailureTimeout {
		s.fault = camstream.CodeSourceUnavailable
		cleanup, done := b.cleanupContext()
		defer done()
		if err := b.cleanSource(cleanup, s); err != nil {
			s.unknown = true
		}
		return
	}
	for _, v := range s.sessions {
		b.pollSession(ctx, s, v, now)
	}
}
func (b *Backend) pollSession(ctx context.Context, s *source, v *session, now time.Time) {
	if v.worker == nil {
		return
	}

	obs, err := b.observe(ctx, v.worker)
	if err != nil {
		v.state.State = camstream.StateUnknown
		v.state.Resources = camstream.PresenceUnknown
	} else {
		v.state.ObservedAt = now
		v.state.Resources = camstream.PresencePresent
		sent, present := obs.viewer()
		if present && sent > v.sent {
			v.sent = sent
			v.lastProgress = now
			v.state.State = camstream.StatePlaying
		}
		if !present && v.answer != nil {
			b.expireSession(s, v, camstream.CodeMediaFailed)
			return
		}
	}
	if v.worker.dead() || now.Sub(v.lastProgress) >= b.cfg.FailureTimeout {
		b.expireSession(s, v, camstream.CodeMediaFailed)
	}
}
func (b *Backend) expireSession(s *source, v *session, code camstream.ErrorCode) {
	ctx, cancel := b.cleanupContext()
	defer cancel()
	if err := b.closeSession(ctx, s, v, code); err != nil {
		s.unknown = true
	}
}
