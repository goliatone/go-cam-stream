//go:build linux || darwin

package viewer

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPrepareManagedRollbackPreservesCredentials(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenPrivateState(ctx, filepath.Join(root, "managed"), "installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	catalog := managedFixture()
	defer catalog.Destroy()
	catalog.Receipts = []ManagedReceipt{
		{OperationKey: catalog.Cameras[0].CreationKey, Owner: "owner", CandidateID: "managed-one", Fingerprint: "creation", ExpiresAt: time.Now().UTC()},
		{OperationKey: strings.Repeat("b", 32), Owner: "owner", CandidateID: "managed-one", Fingerprint: "rotation", ExpiresAt: time.Now().UTC()},
	}
	payload, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	first := ManagedState{Revision: 1, CommitID: "rotation", Payload: payload}
	if outcome, err := store.Commit(ctx, 0, first); err != nil || outcome != CommitDurable {
		t.Fatal("persist fixture", err)
	}
	if removed, err := PrepareManagedRollback(ctx, store); err != nil || removed != 1 {
		t.Fatal("prepare rollback", removed, err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(state.Payload)
	got, err := DecodeManagedCatalog(state)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Destroy()
	if !reflect.DeepEqual(got.Cameras, catalog.Cameras) || !reflect.DeepEqual(got.Credentials, catalog.Credentials) ||
		!reflect.DeepEqual(got.Receipts, catalog.Receipts[:1]) {
		t.Fatal("rollback changed data beyond the update receipt")
	}
	if removed, err := PrepareManagedRollback(ctx, store); err != nil || removed != 0 {
		t.Fatal("rollback preparation was not repeatable", removed, err)
	}
	again, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(again.Payload)
	if !sameManagedState(again, state) {
		t.Fatal("repeated preparation rewrote the store")
	}
}
