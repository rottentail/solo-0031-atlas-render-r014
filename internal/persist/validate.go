package persist

import (
	"atlas-render/internal/asset"
	"atlas-render/internal/model"
	"atlas-render/internal/scene"
	"atlas-render/internal/snapshot"
	"fmt"
)

func Validate(s model.State) error {
	if s.Version != model.FormatVersion {
		return fmt.Errorf("unsupported state version %d", s.Version)
	}
	if s.Scenes == nil || s.Assets == nil || s.Snapshots == nil || s.Jobs == nil || s.Artifacts == nil || s.Receipts == nil {
		return fmt.Errorf("state maps must be present")
	}
	for id, v := range s.Scenes {
		if id != v.ID || !scene.ValidID(id) || v.Revision < 1 || (v.State != "active" && v.State != "archived") {
			return fmt.Errorf("invalid scene record %s", id)
		}
		if e := scene.Validate(v.Document); e != nil {
			return e
		}
	}
	for id, v := range s.Assets {
		if id != v.ID || !scene.ValidID(id) || !asset.KnownState(v.State) {
			return fmt.Errorf("invalid asset record %s", id)
		}
		if e := asset.VerifyRecord(v); e != nil {
			return e
		}
	}
	for id, v := range s.Snapshots {
		if id != v.ID || (v.State != "active" && v.State != "revoked") {
			return fmt.Errorf("invalid snapshot record %s", id)
		}
		if _, ok := s.Scenes[v.SceneID]; !ok {
			return fmt.Errorf("orphan snapshot %s", id)
		}
		if e := ValidateSnapshot(v); e != nil {
			return e
		}
	}
	for id, j := range s.Jobs {
		if id != j.ID || j.SnapshotID != j.Payload.ID || j.Scale < 1 || j.Scale > 2 || j.Attempt < 1 {
			return fmt.Errorf("invalid job %s", id)
		}
		if _, ok := s.Snapshots[j.SnapshotID]; !ok {
			return fmt.Errorf("orphan job %s", id)
		}
		switch j.State {
		case "queued", "running", "failed", "cancelled":
			if j.ArtifactID != "" {
				return fmt.Errorf("unfinished job references artifact")
			}
		case "succeeded":
			a, ok := s.Artifacts[j.ArtifactID]
			if !ok || a.JobID != id {
				return fmt.Errorf("job result missing")
			}
		default:
			return fmt.Errorf("invalid job state")
		}
		if e := ValidateSnapshot(j.Payload); e != nil {
			return e
		}
	}
	for id, a := range s.Artifacts {
		j, ok := s.Jobs[a.JobID]
		if id != a.ID || !ok || j.ArtifactID != id || j.State != "succeeded" {
			return fmt.Errorf("orphan artifact %s", id)
		}
		switch a.State {
		case "live":
			if model.Digest(a.PNG) != a.Digest {
				return fmt.Errorf("artifact digest mismatch")
			}
		case "purged":
			if len(a.PNG) != 0 {
				return fmt.Errorf("purged artifact retains bytes")
			}
		default:
			return fmt.Errorf("invalid artifact state")
		}
	}
	for key, r := range s.Receipts {
		if key != r.Key || r.Fingerprint == "" {
			return fmt.Errorf("invalid receipt")
		}
		if _, ok := s.Jobs[r.JobID]; !ok {
			return fmt.Errorf("receipt result missing")
		}
	}
	return nil
}
func ValidateSnapshot(v model.Snapshot) error {
	return snapshot.Verify(v)
}
