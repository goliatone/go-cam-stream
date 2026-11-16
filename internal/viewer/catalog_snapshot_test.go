package viewer_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

func TestCatalogPublicationPreservesReadsAndAdmission(t *testing.T) {
	service, _, _, catalog := fixture(t)
	revision := api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 1}
	if err := service.PublishCatalog(catalog, revision, []string{"new-manager"}, nil); err != nil {
		t.Fatal(err)
	}
	manager := viewer.WithPrincipal(context.Background(), "new-manager")
	list, err := service.List(manager)
	if err != nil || len(list.Cameras) != 0 || list.Revision == nil {
		t.Fatal("empty manager denied", err)
	}
	if !service.AdmittedPrincipals()["new-manager"] {
		t.Fatal("manager not admitted")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				list, e := service.List(manager)
				if e != nil || list.Revision == nil || list.Revision.RuntimeEpoch != "epoch" {
					t.Error("inconsistent generation", e)
				}
			}
		})
	}
	for generation := uint64(2); generation < 20; generation++ {
		if err := service.PublishCatalog(catalog, api.CatalogRevision{RuntimeEpoch: "epoch", Generation: generation}, []string{"new-manager"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	old := service.CatalogRevision()
	if err := service.PublishCatalog(catalog, api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 20}, []string{"new-manager"}, func() error { return fmt.Errorf("publication refused") }); err == nil {
		t.Fatal("failed preparation published")
	}
	if service.CatalogRevision() != old {
		t.Fatal("failed publication changed generation")
	}
	// Disabling management keeps independent existing grants admitted.
	if err := service.PublishCatalog(catalog, api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 20}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if service.AdmittedPrincipals()["new-manager"] {
		t.Fatal("manager removal ignored")
	}
	for _, a := range catalog.Access {
		if !service.AdmittedPrincipals()[a.Login] {
			t.Fatal("grant admission lost")
		}
	}
}
func TestIdentityReservationPromotionAndAliases(t *testing.T) {
	index := viewer.NewIdentityIndex()
	a := viewer.AddressBinding{Host: "localhost", Port: 18556, Selected: "127.0.0.1", Addresses: []string{"127.0.0.1"}}
	b := viewer.AddressBinding{Host: "::1", Port: 18556, Selected: "::1", Addresses: []string{"::1"}}
	if err := index.Reserve("first", a); err != nil {
		t.Fatal(err)
	}
	if err := index.Reserve("second", b); err == nil {
		t.Fatal("alias admitted")
	}
	if err := index.Promote("first", "saved", a); err != nil {
		t.Fatal("own reservation not promoted", err)
	}
	index.Release("first")
	if err := index.Reserve("second", b); err == nil {
		t.Fatal("promoted identity released")
	}
}
