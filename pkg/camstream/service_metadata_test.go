package camstream_test

import (
	"context"
	"testing"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type constraintStrategy struct{ ownedStrategy }

func (constraintStrategy) Resolve(ctx context.Context, camera cs.Camera, stream cs.StreamID) (cs.StreamSpec, error) {
	spec, err := (ownedStrategy{}).Resolve(ctx, camera, stream)
	if err != nil {
		return spec, err
	}
	switch camera.Config {
	case ownedConfig("negative"):
		spec.Constraints.MaxActiveSources = -1
	case ownedConfig("larger"):
		spec.Constraints.MaxActiveSources = 2
	case ownedConfig("wrong-device"):
		spec.PhysicalCameraID = "foreign"
	case ownedConfig("missing-source"):
		spec.Source = nil
	}
	return spec, nil
}

func TestServiceRejectsInconsistentProviderMetadata(t *testing.T) {
	b := newMemoryBackend()
	s, err := cs.NewService(b, cs.ServiceOptions{}, constraintStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := s.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	for _, mode := range []ownedConfig{"negative", "wrong-device", "missing-source"} {
		camera := testCamera("one", "one")
		camera.Config = mode
		_, err = s.CreateSession(context.Background(), camera, "main", cs.Offer{SDP: "offer"})
		code(t, err, cs.CodeInvalidArgument)
	}
	v := openViewer(t, s, "one", "one", "main")
	alias := testCamera("hall", "one")
	alias.Config = ownedConfig("larger")
	_, err = s.CreateSession(context.Background(), alias, "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeConflict)
	if err = s.CloseSession(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSession(context.Background(), alias, "sub", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeConflict)
}
