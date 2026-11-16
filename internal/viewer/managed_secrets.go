package viewer

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/goliatone/go-cam-stream/config"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	secrets "github.com/goliatone/go-secrets"
)

type SecretResolver interface {
	Resolve(context.Context, secrets.Reference) (*secrets.Value, error)
}

// ManagedSecrets owns draft copies and a committed generation. Resolve transfers
// fresh Values; destroying an encoder's Value cannot destroy the stored binding.
type ManagedSecrets struct {
	mu                sync.RWMutex
	committed, drafts map[string]ManagedCredential
	fallback          SecretResolver
}

func NewManagedSecrets(fallback SecretResolver) *ManagedSecrets {
	return &ManagedSecrets{committed: map[string]ManagedCredential{}, drafts: map[string]ManagedCredential{}, fallback: fallback}
}
func validManagedCredential(c ManagedCredential) bool {
	if len(c.Username) == 0 || len(c.Username) > 1024 || len(c.Password) == 0 || len(c.Password) > 4096 || !utf8.Valid(c.Username) || !utf8.Valid(c.Password) {
		return false
	}
	if strings.ContainsAny(string(c.Username), ":\"\\") {
		return false
	}
	for _, value := range [][]byte{c.Username, c.Password} {
		if strings.ContainsFunc(string(value), unicode.IsControl) {
			return false
		}
	}
	return true
}
func copyCredential(c ManagedCredential) ManagedCredential {
	c.Username = append([]byte(nil), c.Username...)
	c.Password = append([]byte(nil), c.Password...)
	return c
}
func destroyCredentials(values map[string]ManagedCredential) {
	for _, v := range values {
		clear(v.Username)
		clear(v.Password)
	}
}
func (s *ManagedSecrets) PutDraft(c ManagedCredential) error {
	if !config.ValidID(c.ID) || !validManagedCredential(c) {
		return invalid()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.committed[c.ID]; ok {
		return invalid()
	}
	if _, ok := s.drafts[c.ID]; ok {
		return invalid()
	}
	s.drafts[c.ID] = copyCredential(c)
	return nil
}
func (s *ManagedSecrets) RemoveDraft(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.drafts[id]
	clear(c.Username)
	clear(c.Password)
	delete(s.drafts, id)
}
func (s *ManagedSecrets) Publish(credentials []ManagedCredential) error {
	next := map[string]ManagedCredential{}
	for _, c := range credentials {
		if !config.ValidID(c.ID) || !validManagedCredential(c) {
			destroyCredentials(next)
			return invalid()
		}
		if _, ok := next[c.ID]; ok {
			destroyCredentials(next)
			return invalid()
		}
		next[c.ID] = copyCredential(c)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	destroyCredentials(s.committed)
	s.committed = next
	return nil
}
func (s *ManagedSecrets) Resolve(ctx context.Context, ref secrets.Reference) (*secrets.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Scheme() != "managed" {
		if ref.Scheme() == "env" && s.fallback != nil {
			return s.fallback.Resolve(ctx, ref)
		}
		return nil, cs.NewError(cs.CodeAuthenticationFailed, nil)
	}
	u, err := url.Parse(ref.URI())
	if err != nil || !config.ValidID(u.Host) || u.RawQuery != "" || u.RawPath != "" || (u.Path != "/username" && u.Path != "/password") {
		return nil, invalid()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.committed[u.Host]
	if !ok {
		c, ok = s.drafts[u.Host]
	}
	if !ok {
		return nil, cs.NewError(cs.CodeAuthenticationFailed, nil)
	}
	data := c.Password
	if u.Path == "/username" {
		data = c.Username
	}
	return secrets.NewValue(data, secrets.ValueOptions{Version: c.ID})
}
func (s *ManagedSecrets) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	destroyCredentials(s.committed)
	destroyCredentials(s.drafts)
	s.committed = map[string]ManagedCredential{}
	s.drafts = map[string]ManagedCredential{}
}
