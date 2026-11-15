// Package snapshot owns frozen capture. Capture turns a scene revision plus
// its accepted asset closure into a snapshot whose digest covers document and
// asset bytes; Verify checks that a frozen record still matches that digest and
// carries exactly the assets its document references.
package snapshot

import (
	"atlas-render/internal/asset"
	"atlas-render/internal/model"
	"atlas-render/internal/scene"
	"time"
)

// Capture freezes a scene revision into a snapshot. The asset closure is
// resolved from accepted content at the moment of capture, so later scene
// revision or asset retirement cannot alter the captured bytes.
func Capture(id, sceneID string, revision int, doc model.Document, lookup asset.Lookup, at time.Time) (model.Snapshot, error) {
	if e := scene.Validate(doc); e != nil {
		return model.Snapshot{}, e
	}
	closure, e := asset.ReferenceClosure(doc, lookup)
	if e != nil {
		return model.Snapshot{}, e
	}
	v := model.Snapshot{ID: id, SceneID: sceneID, Revision: revision, State: "active", Document: doc, Assets: closure, CreatedAt: at}
	v.Digest = model.SnapshotDigest(v)
	return v, nil
}

// Verify checks a frozen snapshot's document, digest, and asset closure.
func Verify(v model.Snapshot) error {
	if e := scene.Validate(v.Document); e != nil {
		return e
	}
	if model.SnapshotDigest(v) != v.Digest {
		return model.Invalid("snapshot digest mismatch")
	}
	return asset.VerifyClosure(v)
}
