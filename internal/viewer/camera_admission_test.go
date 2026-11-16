package viewer_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

func TestManagementDNSPinAndConcurrentAliasAdmission(t *testing.T) {
	var mu sync.Mutex
	address := "192.168.1.10"
	calls := 0
	lookup := func(context.Context, string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return []netip.Addr{netip.MustParseAddr(address)}, nil
	}
	s, ctx, _ := managementFixture(t, func(o *viewer.ManagementOptions) { o.Lookup = lookup })
	input := draftRequest(1)
	input.Endpoint = "rtsp://camera.local:8554/live"
	d, err := s.CreateDraft(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := viewer.WithPrincipal(ctx, "other")
	input.Mutation = mutation(2)
	input.Endpoint = "rtsp://192.168.1.10:8554/alias"
	other, err := s.CreateDraft(otherCtx, input)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		id   string
		test api.TestResult
		err  error
	}
	results := make(chan result, 2)
	for i, ref := range []struct {
		id  string
		ctx context.Context
	}{{d.ID, ctx}, {other.ID, otherCtx}} {
		go func(i int, id string, actor context.Context) {
			p, e := s.CreateTest(actor, id, api.CreateTestRequest{Mutation: mutation(i + 3), ExpectedRevision: 1, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
			results <- result{id, p, e}
		}(i, ref.id, ref.ctx)
	}
	winner, loser := <-results, <-results
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if winner.err != nil || loser.err == nil {
		t.Fatal("alias admission did not serialize")
	}
	actor := ctx
	if winner.id == other.ID {
		actor = otherCtx
	}
	if _, err = s.CancelTest(actor, winner.id, winner.test.ID, mutation(5)); err != nil {
		t.Fatal(err)
	}
	// Test the logical hostname before rotating DNS; repeated tests keep the pin.
	first, err := s.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(6), ExpectedRevision: 1, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelTest(ctx, d.ID, first.ID, mutation(7)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	address = "192.168.1.20"
	before := calls
	mu.Unlock()
	repeated, err := s.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(8), ExpectedRevision: 1, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.Draft(ctx, d.ID)
	if err != nil || review.DialAddress != "192.168.1.10" {
		t.Fatal("DNS repointed draft", err)
	}
	mu.Lock()
	after := calls
	mu.Unlock()
	if after != before {
		t.Fatal("pinned test re-resolved hostname")
	}
	if _, err = s.CancelTest(ctx, d.ID, repeated.ID, mutation(9)); err != nil {
		t.Fatal(err)
	}
}

func TestManagementRestartLosesUnsavedDraftAndOperations(t *testing.T) {
	s, ctx, _ := managementFixture(t)
	d, p := startManagementPreview(t, s, ctx)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) { o.Epoch = "new-epoch" })
	if _, err := fresh.Operation(actor, mutation(1).OperationKey, "", "epoch"); api.ManagementPublicError(err).Error.Code != string(api.CodeDraftLost) {
		t.Fatal("restart did not report draft loss", err)
	}
	if _, err := fresh.Draft(actor, d.ID); err == nil {
		t.Fatal("unsaved draft survived restart")
	}
	if _, err := fresh.CompleteTest(actor, d.ID, p.ID, api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: 1}); api.ManagementPublicError(err).Error.Code != string(api.CodeDraftLost) {
		t.Fatal("old completion epoch accepted", err)
	}
}
