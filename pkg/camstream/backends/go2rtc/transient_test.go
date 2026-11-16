package go2rtc

import (
	"context"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"testing"
)

func TestTransientRetirementKeepsOrdinaryTombstones(t *testing.T) {
	b, err := New(testConfig(t), testEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	src := &source{removed: true, gate: make(chan struct{}, 1), sessions: map[cs.SessionID]*session{}}
	b.sessions["probe"] = &session{source: src, spec: cs.SessionSpec{ID: "probe", SourceKey: "probe"}, state: cs.SessionState{Resources: cs.PresenceAbsent}}
	b.sessions["ordinary"] = &session{source: src, spec: cs.SessionSpec{ID: "ordinary", SourceKey: "other"}, state: cs.SessionState{Resources: cs.PresenceAbsent}}
	if err = b.RetireTransientSessions(context.Background(), "probe", []cs.SessionID{"ordinary"}); err == nil {
		t.Fatal("foreign ID accepted")
	}
	if err = b.RetireTransientSessions(context.Background(), "probe", []cs.SessionID{"probe"}); err != nil {
		t.Fatal(err)
	}
	if b.sessions["probe"] != nil || b.sessions["ordinary"] == nil {
		t.Fatal("wrong metadata retired")
	}
}
