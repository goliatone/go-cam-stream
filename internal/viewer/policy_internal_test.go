package viewer

import (
	"testing"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func TestSummarizeAvailabilityPrecedence(t *testing.T) {
	for _, tc := range []struct {
		states []cs.State
		want   cs.State
	}{
		{[]cs.State{cs.StateIdle}, cs.StateIdle},
		{[]cs.State{cs.StateIdle, cs.StateUnknown}, cs.StateUnknown},
		{[]cs.State{cs.StateIdle, cs.StateClosing}, cs.StateUnknown},
		{[]cs.State{cs.StateClosed}, cs.StateUnknown},
		{[]cs.State{cs.StateUnknown}, cs.StateUnknown},
		{[]cs.State{cs.StateIdle, cs.StateConnecting}, cs.StateConnecting},
		{[]cs.State{cs.StateConnecting, cs.StateReconnecting}, cs.StateReconnecting},
		{[]cs.State{cs.StateReconnecting, cs.StateOffline}, cs.StateOffline},
		{[]cs.State{cs.StateOffline, cs.StateFailed}, cs.StateFailed},
		{[]cs.State{cs.StateFailed, cs.StateUnknown}, cs.StateFailed},
		{[]cs.State{cs.StateFailed, cs.StatePlaying}, cs.StatePlaying},
		{[]cs.State{cs.StateUnknown, cs.StatePlaying}, cs.StatePlaying},
	} {
		seen := map[cs.State]bool{}
		for _, state := range tc.states {
			seen[state] = true
		}
		if got := summarize(seen); got != tc.want {
			t.Errorf("summarize(%v) = %s, want %s", tc.states, got, tc.want)
		}
	}
}
