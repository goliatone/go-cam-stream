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
	keys, err := binding.IdentityKeys()
	if err != nil || draft == "" {
		return invalid()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, key := range keys {
		if i.committed[key] != "" || (i.reserved[key] != "" && i.reserved[key] != draft) {
			return duplicateCamera()
		}
	}
	// A new binding can replace only an explicitly released reservation.
	if old := i.owners[draft]; old != nil && !equalKeys(old, keys) {
		return invalid()
	}
	for _, key := range keys {
		i.reserved[key] = draft
	}
	i.owners[draft] = keys
	return nil
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
