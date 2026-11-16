package viewer

import (
	"context"
	"reflect"
	"slices"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type catalogSnapshot struct {
	cameras  map[cs.CameraID]config.CatalogCamera
	order    []cs.CameraID
	aliases  map[cs.SourceKey][]api.StreamRef
	access   map[string]map[scope]rights
	managers map[string]bool
	revision api.CatalogRevision
}
type catalogContextKey struct{ service *CameraService }

func newCatalogSnapshot(catalog config.Catalog, revision api.CatalogRevision, managers []string) (*catalogSnapshot, error) {
	state := &catalogSnapshot{cameras: map[cs.CameraID]config.CatalogCamera{}, aliases: map[cs.SourceKey][]api.StreamRef{}, access: map[string]map[scope]rights{}, managers: map[string]bool{}, revision: revision}
	if err := state.loadCatalog(catalog); err != nil {
		return nil, err
	}
	for _, login := range managers {
		if !config.ValidLogin(login) {
			return nil, invalid()
		}
		state.managers[login] = true
	}
	return state, nil
}
func (s *CameraService) captureCatalog(ctx context.Context) context.Context {
	return context.WithValue(ctx, catalogContextKey{s}, s.catalogState.Load())
}
func (s *CameraService) snapshot(ctx context.Context) *catalogSnapshot {
	if v, ok := ctx.Value(catalogContextKey{s}).(*catalogSnapshot); ok {
		return v
	}
	return s.catalogState.Load()
}
func (s *CameraService) CatalogRevision() api.CatalogRevision { return s.catalogState.Load().revision }
func (s *CameraService) AdmittedPrincipals() map[string]bool {
	snapshot := s.catalogState.Load()
	out := map[string]bool{}
	for login := range snapshot.access {
		out[login] = true
	}
	for login := range snapshot.managers {
		out[login] = true
	}
	return out
}

// PublishCatalog atomically replaces a prevalidated immutable generation. Caller
// owns the management mutation gate and has already established durability.
// beforePublish installs only additive secret bindings before new camera IDs can
// be admitted. Existing source/config/grant definitions cannot change here.
func (s *CameraService) PublishCatalog(catalog config.Catalog, revision api.CatalogRevision, managers []string, beforePublish func() error) error {
	next, err := newCatalogSnapshot(catalog, revision, managers)
	if err != nil {
		return err
	}
	s.publication.Lock()
	defer s.publication.Unlock()
	old := s.catalogState.Load()
	if revision.RuntimeEpoch == "" || revision.Generation == 0 {
		return invalid()
	}
	if old.revision.RuntimeEpoch != "" && (revision.RuntimeEpoch != old.revision.RuntimeEpoch || revision.Generation != old.revision.Generation+1) {
		return api.ManagementFailure(api.CodeCatalogConflict)
	}
	// Add-only publication preserves existing aliases, grants and selection order.
	if len(next.order) < len(old.order) || !slices.Equal(next.order[:len(old.order)], old.order) {
		return invalid()
	}
	for id, camera := range old.cameras {
		candidate, ok := next.cameras[id]
		if !ok || !reflect.DeepEqual(candidate, camera) {
			return invalid()
		}
	}
	for login, grants := range old.access {
		for key, value := range grants {
			if next.access[login][key] != value {
				return invalid()
			}
		}
	}
	if beforePublish != nil {
		if err = beforePublish(); err != nil {
			return err
		}
	}
	s.catalogState.Store(next)
	return nil
}
