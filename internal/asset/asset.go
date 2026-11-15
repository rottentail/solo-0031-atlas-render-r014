// Package asset owns accepted bitmap content: identifier and PNG admission,
// content identity, availability, and reference-closure resolution. Direct
// uploads and exchange-package imports apply these same rules.
package asset

import (
	"atlas-render/internal/codec"
	"atlas-render/internal/model"
	"atlas-render/internal/scene"
	"errors"
)

const (
	StateAvailable = "available"
	StateRetired   = "retired"
)

// ErrContentConflict reports an identifier that is already bound to other
// content, or to a record that no longer accepts references.
var ErrContentConflict = errors.New("asset id is bound to different content")

// ErrUnavailable reports a reference that cannot be resolved to available
// content.
var ErrUnavailable = errors.New("asset is not available")

// Lookup resolves an asset record by identifier.
type Lookup func(id string) (model.Asset, bool)

// Accept admits newly supplied bytes as an available asset record. It applies
// the identifier rule, the PNG input limits, and content addressing.
func Accept(id string, data []byte) (model.Asset, error) {
	if !scene.ValidID(id) {
		return model.Asset{}, model.Invalid("invalid asset id")
	}
	img, e := codec.Decode(data)
	if e != nil {
		return model.Asset{}, e
	}
	return model.Asset{ID: id, Digest: model.Digest(data), State: StateAvailable, Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), PNG: data}, nil
}

// Available reports whether a record may be referenced by new work.
func Available(a model.Asset) bool { return a.State == StateAvailable }

// KnownState reports whether a state belongs to the asset lifecycle.
func KnownState(state string) bool { return state == StateAvailable || state == StateRetired }

// SameContent reports whether two records carry the same content for the same
// identifier.
func SameContent(a, b model.Asset) bool { return a.ID == b.ID && a.Digest == b.Digest }

// Admit applies the single admission rule shared by direct upload and package
// import. An unused identifier accepts the incoming content and becomes
// available. A used identifier accepts only identical, still-available content
// and otherwise reports ErrContentConflict. The second result reports whether
// the caller should store the returned record.
func Admit(existing model.Asset, found bool, incoming model.Asset) (model.Asset, bool, error) {
	if !found {
		incoming.State = StateAvailable
		return incoming, true, nil
	}
	if !Available(existing) || !SameContent(existing, incoming) {
		return model.Asset{}, false, ErrContentConflict
	}
	return existing, false, nil
}

// Retire advances an available record to retired. Retiring an already retired
// record is a no-op. The second result reports whether the caller should store
// the returned record.
func Retire(a model.Asset) (model.Asset, bool) {
	if !Available(a) {
		return a, false
	}
	a.State = StateRetired
	return a, true
}

// ReferenceClosure resolves every asset referenced by doc into the closure new
// work may pin. Each reference must resolve to available content; the result is
// keyed by asset identifier.
func ReferenceClosure(doc model.Document, lookup Lookup) (map[string]model.Asset, error) {
	refs := scene.References(doc)
	out := make(map[string]model.Asset, len(refs))
	for _, id := range refs {
		a, ok := lookup(id)
		if !ok || id != a.ID || !Available(a) {
			return nil, ErrUnavailable
		}
		out[id] = a
	}
	return out, nil
}

// VerifyClosure checks that a frozen snapshot carries exactly the assets its
// document references, with intact content. Availability is not part of this
// check: a snapshot owns the bytes it captured, independently of the lifecycle
// state of the source record.
func VerifyClosure(v model.Snapshot) error {
	refs := scene.References(v.Document)
	if len(refs) != len(v.Assets) {
		return model.Invalid("snapshot asset closure mismatch")
	}
	for _, id := range refs {
		a, ok := v.Assets[id]
		if !ok || id != a.ID {
			return model.Invalid("snapshot asset missing")
		}
		if e := VerifyRecord(a); e != nil {
			return e
		}
	}
	return nil
}

// VerifyRecord checks that a stored record's digest, PNG encoding, and
// dimensions agree.
func VerifyRecord(a model.Asset) error {
	if model.Digest(a.PNG) != a.Digest {
		return model.Invalid("asset digest mismatch")
	}
	img, e := codec.Decode(a.PNG)
	if e != nil {
		return e
	}
	if img.Bounds().Dx() != a.Width || img.Bounds().Dy() != a.Height {
		return model.Invalid("asset dimensions mismatch")
	}
	return nil
}
