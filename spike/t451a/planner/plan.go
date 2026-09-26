package planner

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

type Document struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Repository string     `json:"repository"`
	Path       string     `json:"path"`
	ExecPath   string     `json:"exec_path"`
	Producer   Configured `json:"producer"`
	SHA256     string     `json:"sha256"`
	Bytes      int        `json:"bytes"`
}

type UnitImport struct {
	Path string `json:"path"`
	Unit string `json:"unit"`
}
type Unit struct {
	ID              string       `json:"id"`
	Owner           Configured   `json:"owner"`
	Tool            bool         `json:"tool"`
	Variant         string       `json:"variant"`
	ArchiveLabel    string       `json:"archive_label"`
	ImportPath      string       `json:"import_path"`
	ImportMap       string       `json:"import_map"`
	GoFiles         []string     `json:"go_files"`
	CompiledGoFiles []string     `json:"compiled_go_files"`
	Imports         []UnitImport `json:"imports"`
	SDK             string       `json:"sdk,omitempty"`
	PackageName     string       `json:"package_name"`
	SourceImports   []string     `json:"source_imports"`
	Mode            GoMode       `json:"mode"`
}

type Plan struct {
	Version         string     `json:"version"`
	Targets         []Target   `json:"targets"`
	Units           []Unit     `json:"units"`
	Documents       []Document `json:"documents"`
	SDKs            []SDKPlan  `json:"sdks"`
	UniverseSHA256  string     `json:"universe_sha256"`
	MappingSHA256   string     `json:"mapping_sha256"`
	DocumentsSHA256 string     `json:"documents_sha256"`
}

func jsonDigest(v any) string { b, _ := json.Marshal(v); return digest(b) }

// Assemble never creates completeness from a package driver's output. The
// cquery universe and owned aspect projections must agree through aquery's
// exact declared-artifact locator. Extra bytes, objects and edges refuse.
func Assemble(cqueryProto, aqueryProto []byte, projections map[string][]byte) (Plan, error) {
	plan := Plan{Version: "phebs-t451a-plan-v1", Units: []Unit{}, Documents: []Document{}}
	targets, err := DecodeCquery(cqueryProto)
	if err != nil {
		return Plan{}, fmt.Errorf("configured universe: %w", err)
	}
	locs, err := decodeAquery(aqueryProto)
	if err != nil {
		return Plan{}, fmt.Errorf("projection locators: %w", err)
	}
	if len(projections) != len(locs) {
		return Plan{}, errors.New("missing or extra projection files")
	}
	byTarget := map[string]*Target{}
	byLabel := map[string][]Configured{}
	for i := range targets {
		t := &targets[i]
		byTarget[t.key()] = t
		byLabel[t.Label] = append(byLabel[t.Label], t.Configured)
	}
	// Every configured node must belong to the exact requested root closure.
	var queue []Configured
	requestedRoots := map[Configured]bool{}
	for _, root := range Roots() {
		matches := byLabel[canonicalLabel(root)]
		if len(matches) != 1 {
			return Plan{}, errors.New("missing or ambiguous neutral root")
		}
		queue = append(queue, matches[0])
		requestedRoots[matches[0]] = true
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		c := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[c.key()] {
			continue
		}
		seen[c.key()] = true
		t := byTarget[c.key()]
		if t == nil {
			return Plan{}, errors.New("missing closure target")
		}
		queue = append(queue, t.Inputs...)
	}
	if len(seen) != len(targets) {
		return Plan{}, errors.New("extra target outside requested closure")
	}

	// Index the immutable configured graph in reverse. Archive/SDK/producer
	// queries walk consumers of the wanted provider, not unrelated SDK source
	// leaves below the querying target. Each completed ancestor set is reused.
	parents := map[Configured][]Configured{}
	indexedEdges := 0
	for _, target := range targets {
		if len(target.Inputs) > MaxEdges-indexedEdges {
			return Plan{}, errors.New("configured reverse index edge limit")
		}
		indexedEdges += len(target.Inputs)
		for _, input := range target.Inputs {
			parents[input] = append(parents[input], target.Configured)
		}
	}
	visits := 0
	charge := func(count int) bool {
		if count > MaxEdges*8-visits {
			return false
		}
		visits += count
		return true
	}
	ancestors := map[Configured]map[Configured]bool{}
	reachable := func(owner, wanted Configured) (bool, error) {
		// Repeated lookups still cost one bounded unit of work. Cache storage
		// is bounded by charged queue insertions, including duplicate edges.
		if !charge(1) {
			return false, errors.New("configured join work limit")
		}
		if complete, ok := ancestors[wanted]; ok {
			return complete[owner], nil
		}
		if !charge(1) {
			return false, errors.New("configured join work limit")
		}
		pending := []Configured{wanted}
		complete := map[Configured]bool{}
		for len(pending) > 0 {
			c := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if complete[c] {
				continue
			}
			complete[c] = true
			incoming := parents[c]
			if !charge(len(incoming)) {
				return false, errors.New("configured join work limit")
			}
			pending = append(pending, incoming...)
		}
		// Never publish partial membership, including a partial negative.
		ancestors[wanted] = complete
		return complete[owner], nil
	}
	resolve := func(owner Configured, wanted string) (Configured, error) {
		var found []Configured
		for _, candidate := range byLabel[canonicalLabel(wanted)] {
			matches, err := reachable(owner, candidate)
			if err != nil {
				return Configured{}, err
			}
			if matches {
				found = append(found, candidate)
			}
		}
		if len(found) != 1 {
			return Configured{}, errors.New("missing or ambiguous configured artifact owner")
		}
		return found[0], nil
	}
	type pendingUnit struct {
		unit    Unit
		archive projectedArchive
		export  string
	}
	var pending []pendingUnit
	sdkByList := map[string][]SDKPlan{}
	byExport := map[string]string{}
	exportOwners := map[string][]Configured{}
	ownerProjections := map[string]projection{}
	forwardProjections := map[string]ArchiveRef{}
	projectedOwners := map[string]bool{}
	documents := map[string]Document{}
	projectionBytes := 0
	for _, loc := range locs {
		t := byTarget[loc.Owner.key()]
		if t == nil {
			return Plan{}, errors.New("projection owner outside configured universe")
		}
		if projectedOwners[loc.Owner.key()] {
			return Plan{}, errors.New("duplicate owner projection")
		}
		projectedOwners[loc.Owner.key()] = true
		data, ok := projections[loc.Path]
		if !ok {
			return Plan{}, errors.New("missing declared projection")
		}
		projectionBytes += len(data)
		if projectionBytes > MaxProtoBytes {
			return Plan{}, errors.New("aggregate projection byte limit")
		}
		var p projection
		if err := strictJSON(data, &p); err != nil {
			return Plan{}, fmt.Errorf("owned projection: %w", err)
		}
		if p.Version != "phebs-t451a-projection-v1" || canonicalLabel(p.Owner) != loc.Owner.Label || len(p.Archives) > 64 || len(p.Embeds) > 256 {
			return Plan{}, errors.New("projection owner or shape mismatch")
		}
		if p.Forward != nil {
			if p.SDK != nil || len(p.Archives) != 0 || len(p.Roots) != 0 || len(p.Embeds) != 0 || !label(p.Forward.Owner) || canonicalLabel(p.Forward.Owner) == loc.Owner.Label || !relative(p.Forward.Export) || !strings.HasPrefix(p.Forward.Export, "bazel-out/") {
				return Plan{}, errors.New("invalid forwarded archive owner")
			}
			forwardProjections[loc.Owner.key()] = *p.Forward
			continue
		}
		if p.SDK != nil {
			if len(p.Archives) != 0 || len(p.Roots) != 0 || len(p.Embeds) != 0 || canonicalLabel(p.SDK.List.Owner) != loc.Owner.Label {
				return Plan{}, errors.New("invalid SDK projection owner")
			}
			if err := validateSDK(*p.SDK); err != nil {
				return Plan{}, err
			}
			sdk := SDKPlan{ID: jsonDigest([]string{loc.Owner.Label, loc.Owner.Configuration, p.SDK.List.Path}), Owner: loc.Owner, SDKProjection: *p.SDK}
			plan.SDKs = append(plan.SDKs, sdk)
			sdkByList[sdk.Owner.Label+"|"+sdk.List.Path] = append(sdkByList[sdk.Owner.Label+"|"+sdk.List.Path], sdk)
			continue
		}
		if len(p.Roots) != 1 {
			return Plan{}, errors.New("invalid archive root count")
		}
		ownerProjections[loc.Owner.key()] = p
		for _, a := range p.Archives {
			if len(pending) >= MaxUnits || a.Name == "" || len(a.Name) > 4096 || !label(a.Label) || !relative(a.Export) || a.ImportPath == "" || len(a.ImportPath) > 4096 || len(a.Imports) > MaxUnits || len(a.GoFiles) > MaxDocuments || len(a.CompiledGoFiles) > MaxDocuments {
				return Plan{}, errors.New("invalid package-load unit")
			}
			variant := a.Name + "|" + a.Export
			u := Unit{Owner: loc.Owner, Tool: t.Tool, Variant: variant, ArchiveLabel: canonicalLabel(a.Label), ImportPath: a.ImportPath, ImportMap: a.ImportMap, GoFiles: []string{}, CompiledGoFiles: []string{}, Imports: []UnitImport{}, PackageName: a.PackageName, SourceImports: a.SourceImports}
			u.Mode = a.Mode
			u.ID = jsonDigest([]string{loc.Owner.Label, loc.Owner.Configuration, variant})
			exportKey := loc.Owner.key() + "|" + a.Export
			if byExport[exportKey] != "" {
				return Plan{}, errors.New("duplicate package archive")
			}
			byExport[exportKey] = u.ID
			exportOwners[loc.Owner.Label+"|"+a.Export] = append(exportOwners[loc.Owner.Label+"|"+a.Export], loc.Owner)
			for which, files := range [][]File{a.GoFiles, a.CompiledGoFiles} {
				set := map[string]bool{}
				for _, f := range files {
					if !relative(f.Path) || !label(f.Owner) || f.Tree || !strings.HasSuffix(f.Path, ".go") || !checksum(f.SHA256) || f.Bytes < 0 || f.Bytes > MaxFileBytes {
						return Plan{}, errors.New("invalid canonical document")
					}
					repo, rel, err := documentLocation(f.Artifact)
					if err != nil {
						return Plan{}, err
					}
					d := Document{Kind: "source", Repository: repo, Path: rel, ExecPath: f.Path, SHA256: f.SHA256, Bytes: f.Bytes}
					if !f.Source {
						d.Kind = "generated"
						d.Producer, err = resolve(loc.Owner, f.Owner)
						if err != nil {
							return Plan{}, err
						}
					} else {
						if byTarget[(Configured{Label: canonicalLabel(f.Owner)}).key()] == nil {
							return Plan{}, errors.New("source document absent from configured source universe")
						}
						if repo != "" {
							d.Kind = "external"
						}
					}
					d.ID = jsonDigest([]string{d.Kind, d.Repository, d.Path, d.Producer.Label, d.Producer.Configuration})
					if set[d.ID] {
						return Plan{}, errors.New("duplicate document in package")
					}
					set[d.ID] = true
					if old, ok := documents[d.ID]; ok && old != d {
						return Plan{}, errors.New("conflicting canonical document")
					}
					documents[d.ID] = d
					if len(documents) > MaxDocuments {
						return Plan{}, errors.New("plan document limit")
					}
					if which == 0 {
						u.GoFiles = append(u.GoFiles, d.ID)
					} else {
						u.CompiledGoFiles = append(u.CompiledGoFiles, d.ID)
					}
				}
			}
			sort.Strings(u.GoFiles)
			sort.Strings(u.CompiledGoFiles)
			pending = append(pending, pendingUnit{u, a, exportKey})
		}
	}
	for _, p := range pending {
		u := p.unit
		if ref := p.archive.Stdlib; ref != nil {
			for _, sdk := range sdkByList[canonicalLabel(ref.Owner)+"|"+ref.List] {
				matches, err := reachable(u.Owner, sdk.Owner)
				if err != nil {
					return Plan{}, err
				}
				if matches {
					if u.SDK != "" {
						return Plan{}, errors.New("ambiguous SDK projection")
					}
					u.SDK = sdk.ID
				}
			}
			if u.SDK == "" {
				return Plan{}, errors.New("missing exact SDK projection")
			}
		}
		seenImports := map[string]bool{}
		for _, im := range p.archive.Imports {
			if im.Path == "" || len(im.Path) > 4096 || !label(im.Owner) || !relative(im.Export) || seenImports[im.Path] {
				return Plan{}, errors.New("invalid direct package edge")
			}
			seenImports[im.Path] = true
			var owners []Configured
			for _, owner := range exportOwners[canonicalLabel(im.Owner)+"|"+im.Export] {
				matches, err := reachable(u.Owner, owner)
				if err != nil {
					return Plan{}, err
				}
				if matches {
					owners = append(owners, owner)
				}
			}
			if len(owners) != 1 {
				return Plan{}, errors.New("missing or ambiguous declared archive owner")
			}
			owner := owners[0]
			id := byExport[owner.key()+"|"+im.Export]
			if id == "" {
				return Plan{}, errors.New("missing direct package-load unit")
			}
			u.Imports = append(u.Imports, UnitImport{im.Path, id})
		}
		sort.Slice(u.Imports, func(i, j int) bool { return u.Imports[i].Path < u.Imports[j].Path })
		plan.Units = append(plan.Units, u)
	}
	for key, p := range ownerProjections {
		t := byTarget[key]
		seenRoots := map[string]bool{}
		for _, exp := range p.Roots {
			if !relative(exp) || seenRoots[exp] {
				return Plan{}, errors.New("invalid target package edge")
			}
			seenRoots[exp] = true
			id := byExport[key+"|"+exp]
			if id == "" {
				return Plan{}, errors.New("root archive absent from owner projection")
			}
			t.Units = append(t.Units, id)
		}
	}
	for key, ref := range forwardProjections {
		t := byTarget[key]
		var owners []Configured
		for _, owner := range exportOwners[canonicalLabel(ref.Owner)+"|"+ref.Export] {
			matches, err := reachable(t.Configured, owner)
			if err != nil {
				return Plan{}, err
			}
			if matches {
				owners = append(owners, owner)
			}
		}
		if len(owners) != 1 {
			return Plan{}, errors.New("missing or ambiguous forwarded archive owner")
		}
		id := byExport[owners[0].key()+"|"+ref.Export]
		if id == "" {
			return Plan{}, errors.New("forwarded archive package absent")
		}
		t.Units = []string{id}
	}
	for key, p := range ownerProjections {
		t := byTarget[key]
		// Embedding is many-target-to-one-package authority. Only explicit
		// configured direct dependencies may receive the embedding target's unit.
		for _, embed := range p.Embeds {
			if !label(embed) {
				return Plan{}, errors.New("invalid embed label")
			}
			var matches []*Target
			for _, in := range t.Inputs {
				if in.Label == canonicalLabel(embed) {
					matches = append(matches, byTarget[in.key()])
				}
			}
			if len(matches) != 1 {
				return Plan{}, errors.New("ambiguous configured embed")
			}
			if _, ownsArchive := ownerProjections[matches[0].key()]; ownsArchive {
				// A compiled library has its own load unit. Embedding its sources
				// does not rewrite that target's native package identity.
				continue
			}
			if t.Kind == "go_test" {
				return Plan{}, errors.New("source-only test embed needs an explicit archive projection")
			}
			matches[0].Units = append(matches[0].Units, t.Units...)
		}
	}
	// Native aliases and the one owned split wrapper forward configured units.
	// Memoization visits each wrapper once; cycles, depth and total edges refuse.
	forwarded := map[string]bool{}
	active := map[string]bool{}
	mappingEdges := 0
	var forward func(*Target, int) error
	forward = func(t *Target, depth int) error {
		_, explicit := forwardProjections[t.key()]
		if explicit || forwarded[t.key()] || (t.Kind != "alias" && t.Kind != "phebs_split") {
			return nil
		}
		if active[t.key()] || depth > 128 {
			return errors.New("cyclic or deep package forwarding")
		}
		active[t.key()] = true
		ids := map[string]bool{}
		for _, in := range t.Inputs {
			dep := byTarget[in.key()]
			if err := forward(dep, depth+1); err != nil {
				return err
			}
			for _, id := range dep.Units {
				if !ids[id] {
					mappingEdges++
					if mappingEdges > MaxEdges {
						return errors.New("target/package edge limit")
					}
					ids[id] = true
				}
			}
		}
		t.Units = t.Units[:0]
		for id := range ids {
			t.Units = append(t.Units, id)
		}
		t.Units = unique(t.Units)
		active[t.key()] = false
		forwarded[t.key()] = true
		return nil
	}
	for i := range targets {
		if err := forward(&targets[i], 0); err != nil {
			return Plan{}, err
		}
	}
	for i := range targets {
		t := &targets[i]
		t.Units = unique(t.Units)
		if requestedRoots[t.Configured] && len(t.Units) == 0 {
			return Plan{}, errors.New("requested root has no package-load unit")
		}
		switch t.Kind {
		case "go_library", "go_binary", "go_test", "go_proto_library", "go_source", "phebs_split":
			if len(t.Units) == 0 {
				return Plan{}, fmt.Errorf("configured %s has no package-load unit", t.Kind)
			}
		}
	}
	if len(plan.Units) == 0 || len(documents) == 0 {
		return Plan{}, errors.New("empty package/document plan")
	}
	unitMap := map[string]Unit{}
	for _, u := range plan.Units {
		unitMap[u.ID] = u
	}
	var unitQueue []string
	for _, t := range targets {
		unitQueue = append(unitQueue, t.Units...)
	}
	unitSeen := map[string]bool{}
	for len(unitQueue) > 0 {
		id := unitQueue[len(unitQueue)-1]
		unitQueue = unitQueue[:len(unitQueue)-1]
		if unitSeen[id] {
			continue
		}
		unitSeen[id] = true
		for _, im := range unitMap[id].Imports {
			unitQueue = append(unitQueue, im.Unit)
		}
	}
	if len(unitSeen) != len(plan.Units) {
		return Plan{}, errors.New("extra package unit outside target package closure")
	}
	plan.Targets = targets
	for _, d := range documents {
		plan.Documents = append(plan.Documents, d)
	}
	sort.Slice(plan.Units, func(i, j int) bool { return plan.Units[i].ID < plan.Units[j].ID })
	sort.Slice(plan.Documents, func(i, j int) bool { return plan.Documents[i].ID < plan.Documents[j].ID })
	sort.Slice(plan.SDKs, func(i, j int) bool { return plan.SDKs[i].ID < plan.SDKs[j].ID })
	universe := make([]Target, len(targets))
	copy(universe, targets)
	for i := range universe {
		universe[i].Units = nil
	}
	plan.UniverseSHA256 = jsonDigest(universe)
	plan.MappingSHA256 = jsonDigest(struct {
		Targets []Target
		Units   []Unit
		SDKs    []SDKPlan
	}{targets, plan.Units, plan.SDKs})
	plan.DocumentsSHA256 = jsonDigest(struct {
		Documents []Document
		SDKs      []SDKPlan
	}{plan.Documents, plan.SDKs})
	return plan, nil
}

func unique(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func documentLocation(a Artifact) (string, string, error) {
	owner := canonicalLabel(a.Owner)
	at := strings.Index(owner, "//")
	if at < 2 {
		return "", "", errors.New("invalid document repository")
	}
	repo := owner[2:at]
	rel := a.ShortPath
	if repo != "" {
		prefix := "../" + repo + "/"
		if !strings.HasPrefix(rel, prefix) {
			return "", "", errors.New("external artifact lacks canonical repository prefix")
		}
		rel = strings.TrimPrefix(rel, prefix)
	}
	if !relative(rel) || path.Ext(rel) != ".go" {
		return "", "", errors.New("invalid canonical document path")
	}
	if a.Source {
		expected := rel
		if repo != "" {
			expected = "external/" + repo + "/" + rel
		}
		if a.Path != expected {
			return "", "", errors.New("source artifact path disagrees with repository identity")
		}
	} else if !strings.HasPrefix(a.Path, "bazel-out/") {
		return "", "", errors.New("generated artifact is outside Bazel outputs")
	}
	return repo, rel, nil
}
