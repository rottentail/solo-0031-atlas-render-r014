package service

import (
	"atlas-render/internal/asset"
	"atlas-render/internal/model"
)

func (s *Service) ImportAsset(id string, data []byte) (model.Asset, error) {
	var out model.Asset
	incoming, e := asset.Accept(id, data)
	if e != nil {
		return out, e
	}
	e = s.Store.Update(func(st *model.State) error {
		existing, found := st.Assets[id]
		admitted, store, e := asset.Admit(existing, found, incoming)
		if e != nil {
			return model.Conflict("asset id already exists")
		}
		out = admitted
		if !store {
			return nil
		}
		st.Assets[id] = admitted
		st.Emit("asset.imported", id)
		return nil
	})
	return out, e
}
func (s *Service) RetireAsset(id string) error {
	return s.Store.Update(func(st *model.State) error {
		a, ok := st.Assets[id]
		if !ok {
			return model.Missing("asset not found")
		}
		retired, changed := asset.Retire(a)
		if !changed {
			return nil
		}
		st.Assets[id] = retired
		st.Emit("asset.retired", id)
		return nil
	})
}

// assetLookup resolves assets inside one state transaction.
func assetLookup(st *model.State) asset.Lookup {
	return func(id string) (model.Asset, bool) {
		a, ok := st.Assets[id]
		return a, ok
	}
}
