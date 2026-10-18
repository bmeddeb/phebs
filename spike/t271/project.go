package t271

import (
	"encoding/json"
	"slices"
	"strings"
)

func publish(source Source, raw []byte, manifest *validatedManifest) Result {
	result := emptyResult(source)
	result.State, result.Format, result.SourceClass = "accepted", format, manifest.sourceClass
	result.BlobSHA256 = digest(raw)
	type pair struct {
		fact     Fact
		citation Citation
	}
	pairs := make([]pair, 0, len(manifest.declarations))
	viewPositions := make(map[string]int)
	for _, declaration := range manifest.declarations {
		meaning, err := json.Marshal(declaration.meaning)
		if err != nil {
			return publicationRefusal(source)
		}
		identity, err := json.Marshal(struct {
			Repository   string `json:"repository"`
			ManifestPath string `json:"manifest_path"`
			Format       string `json:"format"`
			Kind         string `json:"kind"`
			Owner        *Owner `json:"owner"`
			Declaration  any    `json:"declaration"`
		}{Repository: source.Repository, ManifestPath: source.ManifestPath, Format: format,
			Kind: declaration.kind, Owner: declaration.owner, Declaration: declaration.identity})
		if err != nil {
			return publicationRefusal(source)
		}
		id := digest(identity)
		if declaration.kind == "DECLARES_VIEW_FIELD" {
			position, ok := declaration.meaning["position"].(int)
			if !ok {
				return publicationRefusal(source)
			}
			viewPositions[id] = position
		}
		pairs = append(pairs, pair{
			fact: Fact{ID: id, Kind: declaration.kind, Owner: declaration.owner, Meaning: meaning},
			citation: Citation{FactID: id, Source: source, BlobSHA256: result.BlobSHA256,
				Pointer: declaration.node.pointer, Start: declaration.node.start, End: declaration.node.end,
				SHA256: digest(raw[declaration.node.start:declaration.node.end])},
		})
	}
	slices.SortFunc(pairs, func(a, b pair) int { return strings.Compare(a.fact.ID, b.fact.ID) })
	for _, pair := range pairs {
		result.Facts = append(result.Facts, pair.fact)
		result.Citations = append(result.Citations, pair.citation)
	}
	type ownerKey struct{ kind, name string }
	owners := make(map[ownerKey]*CatalogOwner, len(manifest.owners))
	for _, owner := range manifest.owners {
		owners[ownerKey{kind: owner.kind, name: owner.name}] = &CatalogOwner{
			Name: owner.name, Fields: []Fact{}, Declarations: []Fact{},
		}
	}
	for _, fact := range result.Facts {
		if fact.Kind == "DECLARES_SCHEMA_SET" {
			result.Catalog.SchemaID = fact.ID
			continue
		}
		catalogOwner := owners[ownerKey{kind: fact.Owner.Kind, name: fact.Owner.Name}]
		switch fact.Kind {
		case "DECLARES_TABLE", "DECLARES_VIEW":
			catalogOwner.ID = fact.ID
		case "DECLARES_FIELD", "DECLARES_VIEW_FIELD":
			catalogOwner.Fields = append(catalogOwner.Fields, fact)
		default:
			catalogOwner.Declarations = append(catalogOwner.Declarations, fact)
		}
	}
	for _, owner := range manifest.owners {
		catalogOwner := *owners[ownerKey{kind: owner.kind, name: owner.name}]
		if owner.kind == "table" {
			result.Catalog.Tables = append(result.Catalog.Tables, catalogOwner)
		} else {
			slices.SortFunc(catalogOwner.Fields, func(a, b Fact) int { return viewPositions[a.ID] - viewPositions[b.ID] })
			result.Catalog.Views = append(result.Catalog.Views, catalogOwner)
		}
	}
	for _, collection := range [][]CatalogOwner{result.Catalog.Tables, result.Catalog.Views} {
		slices.SortFunc(collection, func(a, b CatalogOwner) int { return strings.Compare(a.Name, b.Name) })
	}
	return result
}

func publicationRefusal(source Source) Result {
	result := emptyResult(source)
	result.State, result.Refusal = "refused", &Refusal{Code: "invalid_shape"}
	return result
}

type Change struct {
	ID     string `json:"id"`
	Change string `json:"change"`
	Before *Fact  `json:"before,omitempty"`
	After  *Fact  `json:"after,omitempty"`
}

type Comparison struct {
	State        string   `json:"state"`
	Refusal      *Refusal `json:"refusal,omitempty"`
	BeforeSource Source   `json:"before_source"`
	AfterSource  Source   `json:"after_source"`
	BeforeDigest string   `json:"before_digest"`
	AfterDigest  string   `json:"after_digest"`
	SourceClass  string   `json:"source_class"`
	Changes      []Change `json:"changes"`
}

// Compare operates only on two explicitly selected admitted snapshots. IDs and
// meanings exclude commit/digest/citation provenance; both snapshots retain it.
func Compare(before, after Result) Comparison {
	result := Comparison{State: "refused", BeforeSource: before.Source, AfterSource: after.Source,
		BeforeDigest: before.BlobSHA256, AfterDigest: after.BlobSHA256, SourceClass: before.SourceClass,
		Changes: []Change{}}
	fail := func(code string) Comparison {
		result.Refusal = &Refusal{Code: code}
		return result
	}
	if before.State != "accepted" || after.State != "accepted" {
		return fail("invalid_shape")
	}
	if before.Source.Repository != after.Source.Repository || before.Source.ManifestPath != after.Source.ManifestPath || before.Format != after.Format {
		return fail("comparison_lineage_mismatch")
	}
	if before.SourceClass != after.SourceClass {
		return fail("comparison_source_class_mismatch")
	}
	previous := make(map[string]Fact, len(before.Facts))
	current := make(map[string]Fact, len(after.Facts))
	for _, fact := range before.Facts {
		previous[fact.ID] = fact
	}
	for _, fact := range after.Facts {
		current[fact.ID] = fact
		old, exists := previous[fact.ID]
		if !exists {
			result.Changes = append(result.Changes, Change{ID: fact.ID, Change: "added", After: &fact})
		} else if string(old.Meaning) != string(fact.Meaning) {
			result.Changes = append(result.Changes, Change{ID: fact.ID, Change: "changed", Before: &old, After: &fact})
		}
	}
	for _, fact := range before.Facts {
		if _, exists := current[fact.ID]; !exists {
			result.Changes = append(result.Changes, Change{ID: fact.ID, Change: "removed", Before: &fact})
		}
	}
	slices.SortFunc(result.Changes, func(a, b Change) int { return strings.Compare(a.ID, b.ID) })
	result.State = "compared"
	return result
}
