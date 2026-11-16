package go2rtc

import (
	"context"
	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

var _ camstream.TransientRetirer = (*Backend)(nil)

func (b *Backend) RetireTransientSessions(ctx context.Context, key camstream.SourceKey, ids []camstream.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	if b.sources[key] != nil {
		b.mu.Unlock()
		return failure(camstream.CodeConflict, nil)
	}
	// Every supplied record must belong to the same removed source generation.
	var src *source
	for _, id := range ids {
		if v := b.sessions[id]; v != nil {
			if v.spec.SourceKey != key || (src != nil && src != v.source) {
				b.mu.Unlock()
				return failure(camstream.CodeConflict, nil)
			}
			src = v.source
		}
	}
	b.mu.Unlock()
	if src == nil {
		return nil
	}
	// enter rejects removed sources, so acquire the gate directly for terminal
	// metadata inspection. No backend calls or fresh demand run under this gate.
	select {
	case src.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer leave(src)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !src.removed || src.ingest != nil || len(src.sessions) != 0 {
		return failure(camstream.CodeCleanupIncomplete, nil)
	}
	for _, id := range ids {
		if v := b.sessions[id]; v != nil {
			if v.source != src || v.worker != nil || (!v.aborted && v.state.Resources != camstream.PresenceAbsent) {
				return failure(camstream.CodeCleanupIncomplete, nil)
			}
		}
	}
	for _, id := range ids {
		delete(b.sessions, id)
	}
	return nil
}
