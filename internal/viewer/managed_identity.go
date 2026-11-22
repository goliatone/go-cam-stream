package viewer

import "sync"

// IdentityIndex conservatively retains observed base aliases for the process
// lifetime. Management owns resolution before this short in-memory gate; no DNS
// or media I/O occurs while locked. Review-only drafts reserve nothing.
type IdentityIndex struct {
	mu        sync.Mutex
	committed map[string]string
	reserved  map[string]string
	owners    map[string][]string
}

func NewIdentityIndex() *IdentityIndex {
	return &IdentityIndex{committed: map[string]string{}, reserved: map[string]string{}, owners: map[string][]string{}}
}
func (i *IdentityIndex) Observe(owner string, binding AddressBinding) error {
	keys, err := binding.IdentityKeys()
	if err != nil || owner == "" {
		return invalid()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, key := range keys {
		if old := i.committed[key]; old != "" && old != owner {
			return duplicateCamera()
		}
		if i.reserved[key] != "" {
			return duplicateCamera()
		}
	}
	for _, key := range keys {
		i.committed[key] = owner
	}
	return nil
}
func (i *IdentityIndex) Reserve(draft string, binding AddressBinding) error {
	_, err := i.reserve(draft, "", binding)
	return err
}

// reserve returns a committed owner only for a collision with a saved identity.
// The caller must check access before returning it to an operator.
func (i *IdentityIndex) reserve(draft, camera string, binding AddressBinding) (string, error) {
	keys, err := binding.IdentityKeys()
	if err != nil || draft == "" {
		return "", invalid()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	var savedOwner string
	var testConflict bool
	for _, key := range keys {
		if owner := i.committed[key]; owner != "" && owner != camera {
			if savedOwner != "" && savedOwner != owner {
				return "", duplicateCamera()
			}
			savedOwner = owner
		}
		if i.reserved[key] != "" && i.reserved[key] != draft {
			testConflict = true
		}
	}
	if savedOwner != "" || testConflict {
		return savedOwner, duplicateCamera()
	}
	// A new binding can replace only an explicitly released reservation.
	if old := i.owners[draft]; old != nil && !equalKeys(old, keys) {
		return "", invalid()
	}
	for _, key := range keys {
		i.reserved[key] = draft
	}
	i.owners[draft] = keys
	return "", nil
}
func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for j := range a {
		if a[j] != b[j] {
			return false
		}
	}
	return true
}
func (i *IdentityIndex) Release(draft string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, key := range i.owners[draft] {
		if i.reserved[key] == draft {
			delete(i.reserved, key)
		}
	}
	delete(i.owners, draft)
}
func (i *IdentityIndex) Promote(draft, camera string, binding AddressBinding) error {
	keys, err := binding.IdentityKeys()
	if err != nil || camera == "" {
		return invalid()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !equalKeys(i.owners[draft], keys) {
		return invalid()
	}
	for _, key := range keys {
		if i.reserved[key] != draft || i.committed[key] != "" {
			return duplicateCamera()
		}
	}
	for _, key := range keys {
		i.committed[key] = camera
		delete(i.reserved, key)
	}
	delete(i.owners, draft)
	return nil
}

// Forget drops a removed camera's committed keys so that a later camera may use
// its address. Draft reservations stay with their drafts. A managed camera's
// owner is its ID, which never equals a base camera's physical ID.
func (i *IdentityIndex) Forget(camera string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for key, owner := range i.committed {
		if owner == camera {
			delete(i.committed, key)
		}
	}
}

// Replace moves a saved camera to the binding its settings draft reserved: the
// camera's old keys are dropped and the reserved keys are committed to it.
func (i *IdentityIndex) Replace(draft, camera string, binding AddressBinding) error {
	keys, err := binding.IdentityKeys()
	if err != nil || camera == "" {
		return invalid()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !equalKeys(i.owners[draft], keys) {
		return invalid()
	}
	for _, key := range keys {
		if i.reserved[key] != draft || (i.committed[key] != "" && i.committed[key] != camera) {
			return duplicateCamera()
		}
	}
	for key, owner := range i.committed {
		if owner == camera {
			delete(i.committed, key)
		}
	}
	for _, key := range keys {
		i.committed[key] = camera
		delete(i.reserved, key)
	}
	delete(i.owners, draft)
	return nil
}
