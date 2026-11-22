package viewer

import (
	"context"
	"time"
)

// ExpireManagementDraftForTest advances only the selected draft's deadline.
// These test-only hooks keep clock manipulation out of the production API.
func ExpireManagementDraftForTest(s *CameraManagementService, id string) {
	_ = s.acquire(context.Background())
	defer s.release()
	s.drafts[id].review.ExpiresAt = time.Now().Add(-time.Second)
}

// ExpireManagementTestForTest optionally runs the same sweep as Start's timer.
func ExpireManagementTestForTest(s *CameraManagementService, id string, sweep bool) {
	_ = s.acquire(context.Background())
	s.drafts[id].review.Test.ExpiresAt = time.Now().Add(-time.Second)
	s.release()
	if sweep {
		s.expire(context.Background())
	}
}

// FailManagedSyncForTest models a failed post-rename directory durability barrier.
func FailManagedSyncForTest(s *PrivateStateStore) func() {
	s.mu.Lock()
	s.files.syncDirHook = func() error { return storageFailure() }
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.files.syncDirHook = nil
	}
}

// ExpireManagedReceiptsForTest keeps permanent creation anchors intact.
func ExpireManagedReceiptsForTest(s *CameraManagementService) {
	_ = s.acquire(context.Background())
	defer s.release()
	for i := range s.durable.Receipts {
		s.durable.Receipts[i].ExpiresAt = time.Now().Add(-time.Second)
	}
}

// SetManagedRepositoryForTest swaps the repository between operations.
func SetManagedRepositoryForTest(s *CameraManagementService, repository ManagedStateRepository) {
	_ = s.acquire(context.Background())
	defer s.release()
	s.options.Repository = repository
}
