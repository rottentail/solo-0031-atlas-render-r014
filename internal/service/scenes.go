package service

import (
	"atlas-render/internal/asset"
	"atlas-render/internal/model"
	"atlas-render/internal/persist"
	"atlas-render/internal/scene"
	"atlas-render/internal/snapshot"
	"errors"
	"strings"
	"time"
)

type Service struct{ Store *persist.Store }

func (s *Service) PutScene(id, name string, expected int, d model.Document) (model.Scene, error) {
	var out model.Scene
	if !scene.ValidID(id) || strings.TrimSpace(name) == "" || len(name) > 160 {
		return out, model.Invalid("invalid scene id or name")
	}
	if e := scene.Validate(d); e != nil {
		return out, e
	}
	e := s.Store.Update(func(st *model.State) error {
		old, ok := st.Scenes[id]
		if old.Revision != expected {
			return model.Conflict("scene revision changed")
		}
		if ok && old.State != "active" {
			return model.Conflict("scene is archived")
		}
		if _, e := asset.ReferenceClosure(d, assetLookup(st)); e != nil {
			return model.Conflict("scene references unavailable asset")
		}
		out = model.Scene{ID: id, Name: name, Revision: old.Revision + 1, State: "active", Document: d, UpdatedAt: time.Now().UTC()}
		st.Scenes[id] = out
		st.Emit("scene.saved", id)
		return nil
	})
	return out, e
}
func (s *Service) SceneState(id string, expected int, target string) (model.Scene, error) {
	var out model.Scene
	if target != "active" && target != "archived" {
		return out, model.Invalid("unknown scene state")
	}
	e := s.Store.Update(func(st *model.State) error {
		v, ok := st.Scenes[id]
		if !ok {
			return model.Missing("scene not found")
		}
		if v.Revision != expected {
			return model.Conflict("scene revision changed")
		}
		if v.State == target {
			out = v
			return nil
		}
		v.State = target
		v.Revision++
		v.UpdatedAt = time.Now().UTC()
		st.Scenes[id] = v
		st.Emit("scene."+target, id)
		out = v
		return nil
	})
	return out, e
}
func (s *Service) Freeze(id string, expected int) (model.Snapshot, error) {
	var out model.Snapshot
	e := s.Store.Update(func(st *model.State) error {
		v, ok := st.Scenes[id]
		if !ok {
			return model.Missing("scene not found")
		}
		if v.Revision != expected || v.State != "active" {
			return model.Conflict("scene revision or state changed")
		}
		var e error
		out, e = snapshot.Capture(model.ID("snap", st.Sequence+1), id, v.Revision, v.Document, assetLookup(st), time.Now().UTC())
		if e != nil {
			if errors.Is(e, asset.ErrUnavailable) {
				return model.Conflict("snapshot references unavailable asset")
			}
			return e
		}
		st.Snapshots[out.ID] = out
		st.Emit("snapshot.frozen", out.ID)
		return nil
	})
	return out, e
}
func (s *Service) Revoke(id string) error {
	return s.Store.Update(func(st *model.State) error {
		v, ok := st.Snapshots[id]
		if !ok {
			return model.Missing("snapshot not found")
		}
		if v.State == "revoked" {
			return nil
		}
		v.State = "revoked"
		st.Snapshots[id] = v
		st.Emit("snapshot.revoked", id)
		return nil
	})
}
