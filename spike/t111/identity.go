package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Gate 3 extractor: deployed-plane entities and cross-plane identity joins.
// The prime directive is abstention: an identity we cannot pin exactly is
// emitted as tier unresolved with the raw expression preserved — never
// guessed. Logical service / deployable / build target / image / workload /
// runtime service.name stay separate entities; joins are explicit assertions.
//
// Predicates:
//
//	K8S_WORKLOAD            subject=workload(kind/name)  object=manifest source
//	WORKLOAD_RUNS_IMAGE     subject=workload(kind/name)  object=image or template expr
//	K8S_SERVICE_DNS         subject=k8s-service name     object=selector summary
//	IMAGE_BUILD_CONTEXT     subject=image                object=repo-relative build dir
//	SERVICE_NAME_LITERAL    subject=file pkg dir         object=literal or pattern
//
// Import-derived binary reachability is intentionally withheld until every
// import-chain edge can carry pinned evidence of its own.
func extractIdentity(system, commit, root string, _ []Fact) ([]Fact, error) {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	var facts []Fact
	manifestFacts, err := scanManifests(system, commit, root)
	if err != nil {
		return nil, fmt.Errorf("scan manifests: %w", err)
	}
	facts = append(facts, manifestFacts...)
	skaffoldFacts, err := scanSkaffold(system, commit, root)
	if err != nil {
		return nil, fmt.Errorf("scan skaffold: %w", err)
	}
	facts = append(facts, skaffoldFacts...)
	serviceNameFacts, err := scanServiceNameLiterals(system, commit, root)
	if err != nil {
		return nil, fmt.Errorf("scan service.name literals: %w", err)
	}
	facts = append(facts, serviceNameFacts...)
	return facts, nil
}

// ---- K8s manifests: strict YAML first, template scanner as fallback ----

var workloadKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Job": true}

func scanManifests(system, commit, root string) ([]Fact, error) {
	var facts []Fact
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if n == ".git" || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		content, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", path, rerr)
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.Contains(string(content), "kind:") {
			return nil
		}
		if strings.Contains(string(content), "{{") {
			facts = append(facts, scanTemplateManifest(system, commit, root, rel, content)...)
			return nil
		}
		fs2, ok := parseStrictManifest(system, commit, rel, content)
		if !ok {
			return fmt.Errorf("parse manifest candidate %s", rel)
		}
		facts = append(facts, fs2...)
		return nil
	})
	return facts, err
}

type k8sDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Selector map[string]any `yaml:"selector"`
		Template struct {
			Spec struct {
				Containers []struct {
					Name  string `yaml:"name"`
					Image string `yaml:"image"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func parseStrictManifest(system, commit, rel string, content []byte) ([]Fact, bool) {
	var facts []Fact
	digest := blobDigest(content)
	role := classifyRole(rel, content)
	endLine := lineAtOffset(content, len(content))
	// A kustomize deletion patch ($patch: delete) declares a workload only to
	// REMOVE it — asserting existence from it is a false merge.
	if strings.Contains(string(content), "$patch:") {
		return nil, true
	}
	dec := yaml.NewDecoder(strings.NewReader(string(content)))
	for {
		var doc k8sDoc
		if err := dec.Decode(&doc); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, false // templated or malformed — caller falls back
		}
		if doc.Kind == "" || doc.Metadata.Name == "" {
			continue
		}
		if workloadKinds[doc.Kind] {
			w := doc.Kind + "/" + doc.Metadata.Name
			facts = append(facts, newFact("K8S_WORKLOAD", w, rel, role, tierExact,
				system, commit, rel, 0, len(content), 1, endLine, "id-k8s-v1", digest, ""))
			for _, c := range doc.Spec.Template.Spec.Containers {
				// Partial patches (kustomize strategic-merge) list containers
				// without images; an empty image is not a merge claim.
				if c.Image == "" {
					continue
				}
				tier := tierExact
				if strings.Contains(c.Image, "$") {
					tier = tierUnresolved
				}
				facts = append(facts, newFact("WORKLOAD_RUNS_IMAGE", w, c.Image, role, tier,
					system, commit, rel, 0, len(content), 1, endLine, "id-k8s-v1", digest, "container="+c.Name))
			}
		}
		if doc.Kind == "Service" {
			sel := fmt.Sprintf("%v", doc.Spec.Selector)
			facts = append(facts, newFact("K8S_SERVICE_DNS", doc.Metadata.Name, sel, role, tierExact,
				system, commit, rel, 0, len(content), 1, endLine, "id-k8s-v1", digest, ""))
		}
	}
	return facts, true
}

var (
	tplKindRe  = regexp.MustCompile(`(?m)^kind:\s*(\w+)`)
	tplNameRe  = regexp.MustCompile(`(?m)^\s{0,4}name:\s*(.+)$`)
	tplImageRe = regexp.MustCompile(`(?m)^\s*(?:-\s+)?image:\s*"?([^"\n]+)"?`)
)

// scanTemplateManifest extracts workload/image identities from helm-style
// templates without rendering. Template expressions stay unresolved: joining
// values.yaml requires multi-atom evidence, which this spike does not model.
func scanTemplateManifest(system, commit, _ string, rel string, content []byte) []Fact {
	var facts []Fact
	digest := blobDigest(content)
	role := classifyRole(rel, content)
	text := string(content)
	kinds := tplKindRe.FindAllStringSubmatchIndex(text, -1)
	if len(kinds) == 0 {
		return nil
	}
	lineOf := func(off int) int { return 1 + strings.Count(text[:off], "\n") }

	for i, km := range kinds {
		kind := text[km[2]:km[3]]
		if !workloadKinds[kind] && kind != "Service" {
			continue
		}
		end := len(text)
		if i+1 < len(kinds) {
			end = kinds[i+1][0]
		}
		seg := text[km[0]:end]
		segOff := km[0]

		name := ""
		if m := tplNameRe.FindStringSubmatchIndex(seg); m != nil {
			raw := strings.TrimSpace(seg[m[2]:m[3]])
			name, _ = resolveTemplateValue(raw, nil)
		}
		// Render-free scanning cannot prove whether this resource exists after
		// Helm control flow. Even literal fields are therefore abstentions; exact
		// template identity requires a separately pinned, pure renderer.
		subject := kind + "/" + name
		if name == "" {
			subject = kind + "/<unresolved>"
		}
		if workloadKinds[kind] {
			facts = append(facts, newFact("K8S_WORKLOAD", subject, rel, role, tierUnresolved,
				system, commit, rel, segOff, end, lineOf(segOff), lineOf(end), "id-tpl-v1", digest, ""))
			for _, im := range tplImageRe.FindAllStringSubmatchIndex(seg, -1) {
				raw := strings.TrimSpace(seg[im[2]:im[3]])
				img, _ := resolveTemplateValue(raw, nil)
				if img == "" {
					img = raw
				}
				facts = append(facts, newFact("WORKLOAD_RUNS_IMAGE", subject, img, role, tierUnresolved,
					system, commit, rel, segOff+im[0], segOff+im[1], lineOf(segOff+im[0]), lineOf(segOff+im[1]), "id-tpl-v1", digest, "raw="+raw))
			}
		}
	}
	return facts
}

// resolveTemplateValue abstains on every template expression until the output
// format can retain both the template and values-file evidence atoms.
func resolveTemplateValue(raw string, _ map[string]any) (string, string) {
	if !strings.Contains(raw, "{{") {
		return raw, tierExact
	}
	return raw, tierUnresolved
}

// ---- skaffold: exact image → build-context joins (online-boutique) ----

func scanSkaffold(system, commit, root string) ([]Fact, error) {
	var facts []Fact
	for _, name := range []string{"skaffold.yaml", "skaffold.yml"} {
		path := filepath.Join(root, name)
		content, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var doc struct {
			Build struct {
				Artifacts []struct {
					Image   string `yaml:"image"`
					Context string `yaml:"context"`
				} `yaml:"artifacts"`
			} `yaml:"build"`
		}
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		digest := blobDigest(content)
		endLine := lineAtOffset(content, len(content))
		for _, a := range doc.Build.Artifacts {
			if a.Image == "" || a.Context == "" {
				continue
			}
			facts = append(facts, newFact("IMAGE_BUILD_CONTEXT", a.Image, a.Context, roleProduction, tierExact,
				system, commit, name, 0, len(content), 1, endLine, "id-skaffold-v1", digest, ""))
		}
	}
	return facts, nil
}

// ---- OTel service.name literals ----

var svcNameRes = []*regexp.Regexp{
	regexp.MustCompile(`semconv\.ServiceNameKey\.String\(([^)]*)\)`),
	regexp.MustCompile(`attribute\.String\(\s*"service\.name"\s*,\s*([^)]*)\)`),
	regexp.MustCompile(`OTEL_SERVICE_NAME[^\n]*`),
}

func scanServiceNameLiterals(system, commit, root string) ([]Fact, error) {
	var facts []Fact
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if n == ".git" || n == "vendor" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", path, rerr)
		}
		text := string(content)
		if !strings.Contains(text, "service.name") && !strings.Contains(text, "ServiceNameKey") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		digest := blobDigest(content)
		role := classifyRole(rel, content)
		for _, re := range svcNameRes[:2] {
			for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
				arg := strings.TrimSpace(text[m[2]:m[3]])
				val, tier := arg, tierUnresolved
				if strings.HasPrefix(arg, `"`) && strings.HasSuffix(arg, `"`) && !strings.Contains(arg[1:len(arg)-1], `"`) {
					val, tier = arg[1:len(arg)-1], tierExact
				}
				startLine := lineAtOffset(content, m[0])
				endLine := lineAtOffset(content, m[1])
				facts = append(facts, newFact("SERVICE_NAME_LITERAL", filepath.ToSlash(filepath.Dir(rel)), val, role, tier,
					system, commit, rel, m[0], m[1], startLine, endLine, "id-svcname-v1", digest, "arg="+arg))
			}
		}
		return nil
	})
	return facts, err
}
