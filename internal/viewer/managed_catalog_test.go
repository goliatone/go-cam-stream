package viewer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	secrets "github.com/goliatone/go-secrets"
	"github.com/goliatone/go-secrets/providers/environment"
)

func managedFixture() ManagedCatalog {
	return ManagedCatalog{Cameras: []ManagedRecord{{ID: "managed-one", PhysicalID: "device-one", Name: "Camera", Creator: "owner", Binding: AddressBinding{Host: "localhost", Port: 18556, Selected: "127.0.0.1", Addresses: []string{"127.0.0.1"}}, Path: "/macbook", CredentialID: "credential-one", CreationKey: strings.Repeat("a", 32), CreatedAt: time.Now().UTC()}}, Credentials: []ManagedCredential{{ID: "credential-one", Username: []byte("camera"), Password: []byte("private-password")}}, Receipts: []ManagedReceipt{}}
}
func TestManagedCatalogMergeAndSecretOwnership(t *testing.T) {
	ctx := context.Background()
	overlay := managedFixture()
	catalog, err := MergeManagedCatalog(ctx, config.Catalog{}, overlay, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Cameras) != 1 || len(catalog.Access) != 1 || !catalog.Access[0].Grants[0].Reconnect || catalog.Access[0].Login != "owner" {
		t.Fatal("creator access lost")
	}
	cfg := catalog.Cameras[0].Camera.Config.(rtsp.Config)
	if cfg.Host != "127.0.0.1" {
		t.Fatal("logical hostname not pinned")
	}
	resolver := NewManagedSecrets(environment.New(environment.Config{}))
	defer resolver.Close()
	if err = resolver.Publish(overlay.Credentials); err != nil {
		t.Fatal(err)
	}
	overlay.Destroy()
	first, err := resolver.Resolve(ctx, cfg.PasswordRef)
	if err != nil {
		t.Fatal(err)
	}
	borrowed := first.Bytes()
	first.Destroy()
	for _, v := range borrowed {
		if v != 0 {
			t.Fatal("destroy did not clear")
		}
	}
	second, err := resolver.Resolve(ctx, cfg.PasswordRef)
	if err != nil || string(second.Bytes()) != "private-password" {
		t.Fatal("stored credential was borrowed", err)
	}
	second.Destroy()
	if strings.Contains(fmt.Sprintf("%+v %#v", resolver, managedFixture()), "private-password") {
		t.Fatal("credential formatting leaked")
	}
	t.Setenv("CAMSTREAM_TEST_SECRET", "environment-secret")
	ref, _ := secrets.ParseReference("env://CAMSTREAM_TEST_SECRET")
	value, err := resolver.Resolve(ctx, ref)
	if err != nil || string(value.Bytes()) != "environment-secret" {
		t.Fatal("env compatibility", err)
	}
	value.Destroy()
	draft := ManagedCredential{ID: "draft-secret", Username: []byte("draft"), Password: []byte("secret")}
	if err = resolver.PutDraft(draft); err != nil {
		t.Fatal(err)
	}
	draftRef, _ := secrets.ParseReference("managed://draft-secret/password")
	value, err = resolver.Resolve(ctx, draftRef)
	if err != nil {
		t.Fatal(err)
	}
	value.Destroy()
	resolver.RemoveDraft(draft.ID)
	if _, err = resolver.Resolve(ctx, draftRef); err == nil {
		t.Fatal("removed draft still resolved")
	}
}
func TestManagedCatalogConflictsAndCodec(t *testing.T) {
	m := managedFixture()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManagedCatalog(ManagedState{Revision: 1, CommitID: "one", Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Destroy()
	if _, err = MergeManagedCatalog(context.Background(), config.Catalog{Cameras: []config.CatalogCamera{{Camera: cs.Camera{ID: "managed-one"}}}}, m, nil, 2); err == nil {
		t.Fatal("ID collision accepted")
	}
	for _, alias := range []AddressBinding{{Host: "127.0.0.1", Port: 18556, Selected: "127.0.0.1", Addresses: []string{"127.0.0.1"}}, {Host: "::1", Port: 18556, Selected: "::1", Addresses: []string{"::1"}}} {
		if _, err = MergeManagedCatalog(context.Background(), config.Catalog{}, m, []AddressBinding{alias}, 2); err == nil {
			t.Fatal("loopback alias accepted")
		}
	}
	if _, err = MergeManagedCatalog(context.Background(), config.Catalog{}, m, nil, 0); err == nil {
		t.Fatal("capacity ignored")
	}
	m.Cameras = append(m.Cameras, m.Cameras[0])
	m.Cameras[1].ID = "second"
	m.Cameras[1].PhysicalID = "second"
	m.Cameras[1].CredentialID = ""
	m.Cameras[1].CreationKey = strings.Repeat("b", 32)
	if m.Validate() == nil {
		t.Fatal("alias with another path/credentials accepted")
	}
}
func TestManagementPolicy(t *testing.T) {
	p, err := NewManagementPolicy(config.Management{Enabled: true, Principals: []string{"owner"}, DestinationCIDRs: []string{"203.0.113.0/24"}}, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if !p.CanManage(WithPrincipal(context.Background(), "owner")) || p.CanManage(WithPrincipal(context.Background(), "viewer")) {
		t.Fatal("management admission")
	}
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.2", "192.168.0.1", "172.16.0.1", "fd00::1", "100.100.100.100", "203.0.113.2"} {
		if !p.AllowsAddress(netip.MustParseAddr(raw)) {
			t.Fatal("expected admitted", raw)
		}
	}
	for _, raw := range []string{"0.0.0.0", "::", "169.254.169.254", "fe80::1", "224.0.0.1", "ff02::1", "8.8.8.8"} {
		if p.AllowsAddress(netip.MustParseAddr(raw)) {
			t.Fatal("expected denied", raw)
		}
	}
	a := AddressBinding{Host: "camera.local", Port: 554, Selected: "10.0.0.1", Addresses: []string{"10.0.0.1", "10.0.0.2"}}
	b := AddressBinding{Host: "10.0.0.2", Port: 554, Selected: "10.0.0.2", Addresses: []string{"10.0.0.2"}}
	if !BindingOverlap(a, b) {
		t.Fatal("DNS/IP alias not detected")
	}
	b.Port = 8554
	if BindingOverlap(a, b) {
		t.Fatal("port separation lost")
	}
	disabled, _ := NewManagementPolicy(config.Management{}, "installation")
	if disabled.CanManage(WithPrincipal(context.Background(), "owner")) {
		t.Fatal("default management enabled")
	}
}
