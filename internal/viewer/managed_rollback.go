package viewer

import (
	"context"
	"encoding/json"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

// PrepareManagedRollback writes state readable by pre-rotation hosts. Its
// caller owns the stopped installation and the repository's exclusive lock.
func PrepareManagedRollback(ctx context.Context, store ManagedStateRepository) (int, error) {
	state, err := store.Load(ctx)
	if err != nil {
		return 0, err
	}
	defer clear(state.Payload)
	catalog, err := DecodeManagedCatalog(state)
	if err != nil {
		return 0, err
	}
	defer catalog.Destroy()

	anchors := make(map[cs.CameraID]string, len(catalog.Cameras))
	for _, camera := range catalog.Cameras {
		anchors[camera.ID] = camera.CreationKey
	}
	creationReceipts := catalog.Receipts[:0]
	for _, receipt := range catalog.Receipts {
		if receipt.OperationKey == anchors[receipt.CandidateID] {
			creationReceipts = append(creationReceipts, receipt)
		}
	}
	// Older hosts read neither editing receipts nor tombstones. A removed
	// camera's creation receipt left with it, so dropping its tombstone frees
	// nothing that a creation replay could reach.
	removed := len(catalog.Receipts) - len(creationReceipts)
	if removed == 0 && len(catalog.Removed) == 0 {
		return 0, nil
	}
	catalog.Receipts, catalog.Removed = creationReceipts, nil
	payload, err := json.Marshal(catalog)
	if err != nil {
		return 0, storageFailure()
	}
	defer clear(payload)
	next := ManagedState{Revision: state.Revision + 1, CommitID: randomManagementID(), Payload: payload}
	outcome, err := store.Commit(ctx, state.Revision, next)
	if outcome == CommitUnknown {
		selected, recovered, recoverErr := store.Recover(ctx)
		defer clear(selected.Payload)
		if recoverErr == nil && recovered == CommitDurable && sameManagedState(selected, next) {
			return removed, nil
		}
		return 0, unknownSave()
	}
	if err != nil {
		return 0, err
	}
	if outcome != CommitDurable {
		return 0, storageFailure()
	}
	return removed, nil
}
