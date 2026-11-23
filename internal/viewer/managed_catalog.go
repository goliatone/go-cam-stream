package viewer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"slices"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	secrets "github.com/goliatone/go-secrets"
)

// ManagedRecord is private durable metadata. The creation anchor is permanent;
// bounded receipts can expire without permitting a second camera creation.
type ManagedRecord struct {
	ID                 cs.CameraID         `json:"id"`
	PhysicalID         cs.PhysicalCameraID `json:"physical_id"`
	Name               string              `json:"name"`
	Creator            string              `json:"creator"`
	Binding            AddressBinding      `json:"binding"`
	Path               string              `json:"path"`
	Query              string              `json:"query"`
	CredentialID       string              `json:"credential_id,omitempty"`
	ConnectionRevision string              `json:"connection_revision,omitempty"`
	CreationKey        string              `json:"creation_key"`
	CreatedAt          time.Time           `json:"created_at"`
}

func (ManagedRecord) String() string   { return "[managed camera]" }
func (ManagedRecord) GoString() string { return "[managed camera]" }

type ManagedCredential struct {
	ID       string `json:"id"`
	Username []byte `json:"username"`
	Password []byte `json:"password"`
}

func (ManagedCredential) String() string   { return "[managed credential]" }
func (ManagedCredential) GoString() string { return "[managed credential]" }

type ManagedReceipt struct {
	OperationKey    string            `json:"operation_key"`
	Owner           string            `json:"owner"`
	CandidateID     cs.CameraID       `json:"candidate_id"`
	Kind            api.OperationKind `json:"kind,omitempty"` // empty for a save or sign-in change
	Fingerprint     string            `json:"fingerprint"`    // save contains only IDs/revisions, never a credential hash
	ExpiresAt       time.Time         `json:"expires_at"`
	StoppedSessions int               `json:"stopped_sessions,omitempty"`
}

// isSave reports whether a receipt records a draft save rather than a change
// made directly to a saved camera.
func (r ManagedReceipt) isSave() bool { return r.Kind == "" }

// ManagedTombstone keeps a removed camera's creation anchor while a receipt
// still names it, so neither its creation nor its removal can be replayed into
// a second camera. Tombstones leave with their last receipt.
type ManagedTombstone struct {
	ID          cs.CameraID `json:"id"`
	Creator     string      `json:"creator"`
	CreationKey string      `json:"creation_key"`
	RemovedAt   time.Time   `json:"removed_at"`
}

type ManagedCatalog struct {
	Cameras     []ManagedRecord     `json:"cameras"`
	Credentials []ManagedCredential `json:"credentials"`
	Receipts    []ManagedReceipt    `json:"receipts"`
	Removed     []ManagedTombstone  `json:"removed,omitempty"`
}

func (ManagedCatalog) String() string   { return "[managed catalog redacted]" }
func (ManagedCatalog) GoString() string { return "[managed catalog redacted]" }
func DecodeManagedCatalog(state ManagedState) (ManagedCatalog, error) {
	out := ManagedCatalog{Cameras: []ManagedRecord{}, Credentials: []ManagedCredential{}, Receipts: []ManagedReceipt{}}
	if state.Revision == 0 && len(state.Payload) == 0 {
		return out, nil
	}
	if !uniqueJSON(state.Payload) {
		return ManagedCatalog{}, storageFailure()
	}
	d := json.NewDecoder(bytes.NewReader(state.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF {
		return ManagedCatalog{}, storageFailure()
	}
	if err := out.Validate(); err != nil {
		out.Destroy()
		return ManagedCatalog{}, err
	}
	return out, nil
}
func (m ManagedCatalog) Validate() error {
	if m.Cameras == nil || m.Credentials == nil || m.Receipts == nil || len(m.Cameras) > 65535 || len(m.Credentials) > len(m.Cameras) || len(m.Receipts) > api.MaxSaveReceipts || len(m.Removed) > api.MaxSaveReceipts {
		return storageFailure()
	}
	ids := map[cs.CameraID]bool{}
	physical := map[cs.PhysicalCameraID]bool{}
	anchors := map[string]bool{}
	credentials := map[string]bool{}
	used := map[string]bool{}
	for _, c := range m.Credentials {
		if !config.ValidID(c.ID) || credentials[c.ID] || !validManagedCredential(c) {
			return storageFailure()
		}
		credentials[c.ID] = true
	}
	for i, c := range m.Cameras {
		key := c.Creator + "\x00" + c.CreationKey
		if !config.ValidID(string(c.ID)) || !config.ValidID(string(c.PhysicalID)) || !config.ValidLogin(c.Creator) || ids[c.ID] || physical[c.PhysicalID] || anchors[key] || c.Name == "" || len(c.Name) > rtsp.MaxLabelBytes || c.CreatedAt.IsZero() || (api.Mutation{OperationKey: c.CreationKey, RuntimeEpoch: "stored"}).Validate() != nil {
			return storageFailure()
		}
		ids[c.ID] = true
		physical[c.PhysicalID] = true
		anchors[key] = true
		if c.ConnectionRevision != "" && !config.ValidID(c.ConnectionRevision) {
			return storageFailure()
		}
		if c.CredentialID != "" {
			if !credentials[c.CredentialID] || used[c.CredentialID] {
				return storageFailure()
			}
			used[c.CredentialID] = true
		}
		if validateBinding(c.Binding) != nil {
			return storageFailure()
		}
		for _, old := range m.Cameras[:i] {
			if BindingOverlap(old.Binding, c.Binding) {
				return duplicateCamera()
			}
		}
		if _, err := compileManaged(context.Background(), c); err != nil {
			return storageFailure()
		}
	}
	if len(used) != len(credentials) {
		return storageFailure()
	}
	removed := map[cs.CameraID]string{} // tombstone ID -> creator
	for _, t := range m.Removed {
		key := t.Creator + "\x00" + t.CreationKey
		if !config.ValidID(string(t.ID)) || !config.ValidLogin(t.Creator) || ids[t.ID] || removed[t.ID] != "" || anchors[key] || t.RemovedAt.IsZero() || (api.Mutation{OperationKey: t.CreationKey, RuntimeEpoch: "stored"}).Validate() != nil {
			return storageFailure()
		}
		removed[t.ID] = t.Creator
		anchors[key] = true
	}
	receipts := map[string]bool{}
	for _, r := range m.Receipts {
		key := r.Owner + "\x00" + r.OperationKey
		if receipts[key] || (api.Mutation{OperationKey: r.OperationKey, RuntimeEpoch: "stored"}).Validate() != nil || r.Fingerprint == "" || r.ExpiresAt.IsZero() {
			return storageFailure()
		}
		receipts[key] = true
		if r.StoppedSessions < 0 || (r.Kind != api.OperationRemove && r.StoppedSessions != 0) {
			return storageFailure()
		}
		// A removal receipt names its tombstone. A change receipt never reuses a
		// creation key; a save receipt may only reuse its own camera's.
		if r.Kind == api.OperationRemove {
			if removed[r.CandidateID] != r.Owner || anchors[key] {
				return storageFailure()
			}
			continue
		}
		if !ids[r.CandidateID] || (!r.isSave() && (r.Kind != api.OperationRename || anchors[key])) {
			return storageFailure()
		}
		matched := false
		for _, c := range m.Cameras {
			if c.ID == r.CandidateID && c.Creator == r.Owner && (!anchors[key] || c.CreationKey == r.OperationKey) {
				matched = true
			}
		}
		if !matched {
			return storageFailure()
		}
	}
	return nil
}
func (m *ManagedCatalog) Destroy() {
	for i := range m.Credentials {
		clear(m.Credentials[i].Username)
		clear(m.Credentials[i].Password)
	}
	m.Credentials = nil
}
func compileManaged(ctx context.Context, r ManagedRecord) (config.CatalogCamera, error) {
	cfg := rtsp.Config{Host: r.Binding.Selected, Port: r.Binding.Port, Profiles: []rtsp.Profile{{ID: "default", Name: "Default", Path: r.Path, Query: r.Query}}}
	if r.CredentialID != "" {
		cfg.UsernameRef, _ = secrets.ParseReference("managed://" + r.CredentialID + "/username")
		cfg.PasswordRef, _ = secrets.ParseReference("managed://" + r.CredentialID + "/password")
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		return config.CatalogCamera{}, err
	}
	camera := cs.Camera{ID: r.ID, Name: r.Name, PhysicalID: r.PhysicalID, Config: normalized}
	strategy := rtsp.Strategy{}
	streams, err := strategy.Streams(ctx, camera)
	if err != nil {
		return config.CatalogCamera{}, err
	}
	spec, err := strategy.Resolve(ctx, camera, "default")
	if err != nil {
		return config.CatalogCamera{}, err
	}
	return config.CatalogCamera{Camera: camera, Streams: streams, Sources: map[cs.StreamID]cs.SourceKey{"default": spec.Key}, ManagedOwner: r.Creator}, nil
}

// MergeManagedCatalog performs no DNS or media I/O. baseBindings contains fresh
// host-owned identity observations; unresolved base hosts block admission in T02.
func MergeManagedCatalog(ctx context.Context, base config.Catalog, overlay ManagedCatalog, baseBindings []AddressBinding, maxSources int) (config.Catalog, error) {
	if overlay.Validate() != nil {
		return config.Catalog{}, storageFailure()
	}
	out := config.Catalog{Cameras: slices.Clone(base.Cameras), Access: make([]config.Access, len(base.Access))}
	for i, a := range base.Access {
		out.Access[i] = config.Access{Login: a.Login, Grants: slices.Clone(a.Grants)}
		for j := range out.Access[i].Grants {
			out.Access[i].Grants[j].Streams = slices.Clone(a.Grants[j].Streams)
		}
	}
	sources := map[cs.SourceKey]bool{}
	for _, c := range base.Cameras {
		for _, key := range c.Sources {
			sources[key] = true
		}
	}
	for _, r := range overlay.Cameras {
		for _, c := range base.Cameras {
			if c.Camera.ID == r.ID || c.Camera.PhysicalID == r.PhysicalID {
				return config.Catalog{}, duplicateCamera()
			}
		}
		for _, b := range baseBindings {
			if BindingOverlap(b, r.Binding) {
				return config.Catalog{}, duplicateCamera()
			}
		}
		// Literal base addresses and logical host equality require no resolver.
		for _, c := range base.Cameras {
			var host string
			var port uint16
			switch cfg := c.Camera.Config.(type) {
			case rtsp.Config:
				host, port = cfg.Host, cfg.Port
			case ec70.Config:
				host, port = cfg.Host, cfg.Port
			}
			if port == r.Binding.Port && host == r.Binding.Host {
				return config.Catalog{}, duplicateCamera()
			}
			if ip, err := netip.ParseAddr(host); err == nil {
				binding := AddressBinding{Host: host, Port: port, Selected: ip.Unmap().String(), Addresses: []string{ip.Unmap().String()}}
				if BindingOverlap(binding, r.Binding) {
					return config.Catalog{}, duplicateCamera()
				}
			}
		}
		item, err := compileManaged(ctx, r)
		if err != nil {
			return config.Catalog{}, err
		}
		for _, key := range item.Sources {
			if sources[key] {
				return config.Catalog{}, duplicateCamera()
			}
			sources[key] = true
		}
		out.Cameras = append(out.Cameras, item)
		grant := config.Grant{CameraID: r.ID, Streams: []cs.StreamID{"default"}, View: true, Diagnose: true, Reconnect: true}
		index := -1
		for i, a := range out.Access {
			if a.Login == r.Creator {
				index = i
				break
			}
		}
		if index < 0 {
			out.Access = append(out.Access, config.Access{Login: r.Creator, Grants: []config.Grant{grant}})
		} else {
			out.Access[index].Grants = append(out.Access[index].Grants, grant)
		}
	}
	if maxSources < 1 || len(sources) > maxSources {
		return config.Catalog{}, cs.NewError(cs.CodeCapacityExceeded, nil)
	}
	return out, nil
}
