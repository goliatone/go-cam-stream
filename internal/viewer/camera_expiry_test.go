package viewer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	secrets "github.com/goliatone/go-secrets"
)

func TestExpiredDraftDestroysPrivateCredential(t *testing.T) {
	policy, err := NewManagementPolicy(config.Management{Enabled: true, Principals: []string{"manager"}}, "installation")
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewManagedSecrets(nil)
	defer resolver.Close()
	service := &CameraManagementService{gate: make(chan struct{}, 1), options: ManagementOptions{Policy: policy, Secrets: resolver}, epoch: "epoch", drafts: map[string]*cameraDraft{}, operations: map[string]*managementOperation{}, identities: NewIdentityIndex()}
	actor := WithPrincipal(context.Background(), "manager")
	review, err := service.CreateDraft(actor, api.CreateDraftRequest{OperationKey: strings.Repeat("a", 32), RuntimeEpoch: "epoch", Name: "Camera", Endpoint: "rtsp://localhost:18556/macbook", Authentication: api.AuthenticationCredentials, Credentials: &api.Credentials{Username: "camera", Password: "private"}})
	if err != nil {
		t.Fatal(err)
	}
	draft := service.drafts[review.ID]
	credentialID := draft.credential.ID
	borrowed := draft.credential.Password
	draft.review.ExpiresAt = time.Now().Add(-time.Second)
	if _, err = service.Draft(actor, review.ID); err == nil {
		t.Fatal("expired draft readable")
	}
	for _, b := range borrowed {
		if b != 0 {
			t.Fatal("expired credential retained")
		}
	}
	ref, err := secrets.ParseReference("managed://" + credentialID + "/password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resolver.Resolve(actor, ref); err == nil {
		t.Fatal("expired secret resolves")
	}
	if draft.review.State != api.DraftExpired {
		t.Fatal("expiry state lost")
	}
}
