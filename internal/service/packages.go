package service

import (
	"atlas-render/internal/asset"
	"atlas-render/internal/bundle"
	"atlas-render/internal/model"
	"atlas-render/internal/scene"
	"sort"
	"time"
)

func (s *Service) Export(id string) (bundle.Package, error) {
	v, ok := s.Store.Read().Snapshots[id]
	if !ok {
		return bundle.Package{}, model.Missing("snapshot not found")
	}
	return bundle.Export(v), nil
}

// Import commits a validated exchange package as a new active scene in one
// state transaction. Asset admission follows the same rule as direct upload,
// and a rejected commit leaves no asset behind in destination state.
func (s *Service) Import(id, name string, p bundle.Package) (model.Scene, error) {
	var out model.Scene
	if !scene.ValidID(id) || len(name) < 1 || len(name) > 160 {
		return out, model.Invalid("invalid import destination")
	}
	if e := bundle.Validate(p); e != nil {
		return out, e
	}
	e := s.Store.Update(func(st *model.State) error {
		if _, ok := st.Scenes[id]; ok {
			return model.Conflict("destination scene already exists")
		}
		for _, aid := range sortedAssetIDs(p.Snapshot.Assets) {
			incoming := p.Snapshot.Assets[aid]
			existing, found := st.Assets[aid]
			admitted, store, e := asset.Admit(existing, found, incoming)
			if e != nil {
				return model.Conflict("destination asset conflicts with package")
			}
			if store {
				st.Assets[aid] = admitted
			}
		}
		out = model.Scene{ID: id, Name: name, Revision: 1, State: "active", Document: p.Snapshot.Document, UpdatedAt: time.Now().UTC()}
		st.Scenes[id] = out
		st.Emit("scene.imported", id)
		return nil
	})
	return out, e
}

// sortedAssetIDs gives package admission a deterministic order independent of
// map iteration, so which conflict is reported does not vary between attempts.
func sortedAssetIDs(assets map[string]model.Asset) []string {
	ids := make([]string, 0, len(assets))
	for id := range assets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
