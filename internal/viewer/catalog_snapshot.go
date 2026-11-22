package viewer

import (
	"context"
	"reflect"
	"slices"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
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
	return s.publishCatalog(catalog, revision, managers, catalogChange{}, beforePublish)
}

// catalogChange names the one managed camera a publication may alter, and how.
// Everything else stays add-only: other cameras, grants and order are unchanged.
type catalogChange struct {
	camera      cs.CameraID
	credentials bool // credential references and sources may change
	address     bool // host, port, path and query may change too
	rename      bool // the display name may change
	remove      bool // the camera and its grants leave the catalog
}

// checkCatalog applies publication's rules to a prepared generation without
// publishing it, so a change that publication would refuse is never committed.
func (s *CameraService) checkCatalog(catalog config.Catalog, revision api.CatalogRevision, managers []string, change catalogChange) error {
	next, err := newCatalogSnapshot(catalog, revision, managers)
	if err != nil {
		return err
	}
	s.publication.Lock()
	defer s.publication.Unlock()
	return validateCatalogChange(s.catalogState.Load(), next, change)
}

func (s *CameraService) publishCatalog(catalog config.Catalog, revision api.CatalogRevision, managers []string, change catalogChange, beforePublish func() error) error {
	next, err := newCatalogSnapshot(catalog, revision, managers)
	if err != nil {
		return err
	}
	s.publication.Lock()
	defer s.publication.Unlock()
	if err = validateCatalogChange(s.catalogState.Load(), next, change); err != nil {
		return err
	}
	if beforePublish != nil {
		if err = beforePublish(); err != nil {
			return err
		}
	}
	s.catalogState.Store(next)
	return nil
}

// validateCatalogChange is called under the publication lock.
func validateCatalogChange(old, next *catalogSnapshot, change catalogChange) error {
	revision := next.revision
	if revision.RuntimeEpoch == "" || revision.Generation == 0 {
		return invalid()
	}
	if old.revision.RuntimeEpoch != "" && (revision.RuntimeEpoch != old.revision.RuntimeEpoch || revision.Generation != old.revision.Generation+1) {
		return api.ManagementFailure(api.CodeCatalogConflict)
	}
	// Publication preserves existing aliases, grants and selection order; only a
	// removal takes its camera out, and nothing is added alongside it.
	order := old.order
	if change.remove {
		order = slices.DeleteFunc(slices.Clone(old.order), func(id cs.CameraID) bool { return id == change.camera })
		if len(order) == len(old.order) || !slices.Equal(next.order, order) {
			return invalid()
		}
	}
	if len(next.order) < len(order) || !slices.Equal(next.order[:len(order)], order) {
		return invalid()
	}
	for id, camera := range old.cameras {
		candidate, ok := next.cameras[id]
		if change.remove && id == change.camera {
			if ok || camera.ManagedOwner == "" {
				return invalid()
			}
			continue
		}
		if !ok {
			return invalid()
		}
		if id == change.camera {
			currentConfig, currentOK := camera.Camera.Config.(rtsp.Config)
			nextConfig, nextOK := candidate.Camera.Config.(rtsp.Config)
			if camera.ManagedOwner == "" || !currentOK || !nextOK {
				return invalid()
			}
			if change.credentials || change.address {
				currentConfig.UsernameRef, currentConfig.PasswordRef = nextConfig.UsernameRef, nextConfig.PasswordRef
				camera.Sources = candidate.Sources
			}
			if change.address {
				currentConfig.Host, currentConfig.Port, currentConfig.Profiles = nextConfig.Host, nextConfig.Port, nextConfig.Profiles
			}
			if change.rename {
				camera.Camera.Name = candidate.Camera.Name
			}
			camera.Camera.Config = currentConfig
		}
		if !reflect.DeepEqual(candidate, camera) {
			return invalid()
		}
	}
	for login, grants := range old.access {
		for key, value := range grants {
			if change.remove && key.camera == change.camera {
				if _, kept := next.access[login][key]; kept {
					return invalid()
				}
				continue
			}
			if next.access[login][key] != value {
				return invalid()
			}
		}
	}
	return nil
}
