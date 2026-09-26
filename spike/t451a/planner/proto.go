package planner

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// These are neutral-spike refusal bounds, not production admission limits.
const (
	MaxProtoBytes      = 64 << 20
	MaxTargets         = 16384
	MaxEdges           = 131072
	MaxProjections     = 4096
	MaxProjectionBytes = 8 << 20
	MaxUnits           = 8192
	MaxDocuments       = 65536
	MaxFileBytes       = 8 << 20
)

type Configured struct {
	Label         string `json:"label"`
	Configuration string `json:"configuration"`
}

func (c Configured) key() string { return c.Label + "#" + c.Configuration }

type Target struct {
	Configured
	Kind   string       `json:"kind"`
	Tool   bool         `json:"tool"`
	Inputs []Configured `json:"inputs"`
	Units  []string     `json:"units"`
}

type wireField struct {
	n protowire.Number
	t protowire.Type
	b []byte
	v uint64
}

// Read only the pinned Bazel 9.2.0 protobuf fields needed for authority. Unknown
// metadata is skipped, but malformed wire data and duplicate singular fields
// are rejected. The input/output byte limits bound storage before allocation.
func wireFields(b []byte) ([]wireField, error) {
	var out []wireField
	for len(b) > 0 {
		n, t, k := protowire.ConsumeTag(b)
		if k < 0 || n <= 0 || len(out) >= MaxEdges {
			return nil, errors.New("invalid or oversized protobuf")
		}
		b = b[k:]
		f := wireField{n: n, t: t}
		switch t {
		case protowire.BytesType:
			f.b, k = protowire.ConsumeBytes(b)
		case protowire.VarintType:
			f.v, k = protowire.ConsumeVarint(b)
		case protowire.Fixed32Type:
			_, k = protowire.ConsumeFixed32(b)
		case protowire.Fixed64Type:
			_, k = protowire.ConsumeFixed64(b)
		default:
			return nil, errors.New("unsupported protobuf wire type")
		}
		if k < 0 {
			return nil, errors.New("truncated protobuf")
		}
		b = b[k:]
		out = append(out, f)
	}
	return out, nil
}

func scalar(fs []wireField, number protowire.Number, kind protowire.Type) (wireField, error) {
	found := false
	var out wireField
	for _, f := range fs {
		if f.n == number {
			if found || f.t != kind {
				return out, errors.New("duplicate or mistyped protobuf field")
			}
			found, out = true, f
		}
	}
	return out, nil
}

func message(fs []wireField, number protowire.Number) ([]wireField, error) {
	f, err := scalar(fs, number, protowire.BytesType)
	if err != nil {
		return nil, err
	}
	return wireFields(f.b)
}

func textField(fs []wireField, number protowire.Number) (string, error) {
	f, err := scalar(fs, number, protowire.BytesType)
	if err != nil {
		return "", err
	}
	if len(f.b) > 4096 || !utf8.Valid(f.b) || strings.ContainsAny(string(f.b), "\x00\r\n") {
		return "", errors.New("invalid protobuf text")
	}
	return string(f.b), nil
}

func uintField(fs []wireField, number protowire.Number) (uint64, error) {
	f, err := scalar(fs, number, protowire.VarintType)
	if err == nil && f.v > 1<<32-1 {
		err = errors.New("protobuf id overflow")
	}
	return f.v, err
}

func checksum(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func label(s string) bool {
	// --consistent_labels emits @@ canonical labels; main-repository // labels
	// are accepted too because native provider labels can use that spelling.
	if len(s) > 4096 || strings.ContainsAny(s, "\x00\r\n\t \\#") {
		return false
	}
	i := strings.Index(s, "//")
	if i < 0 || (i != 0 && !strings.HasPrefix(s, "@@")) {
		return false
	}
	p := strings.SplitN(s[i+2:], ":", 2)
	return len(p) == 2 && p[1] != "" && !strings.Contains(p[0], "..") && !strings.Contains(p[1], "..")
}

func canonicalLabel(s string) string {
	if strings.HasPrefix(s, "//") {
		return "@@" + s
	}
	return s
}

func relative(s string) bool {
	return s != "" && len(s) <= 4096 && s != "." && !strings.HasPrefix(s, "/") && path.Clean(s) == s && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\x00\r\n\\")
}

type configuration struct {
	id   uint64
	hash string
	tool bool
}

func decodeConfiguration(b []byte) (configuration, error) {
	fs, err := wireFields(b)
	if err != nil {
		return configuration{}, err
	}
	id, err := uintField(fs, 1)
	if err != nil {
		return configuration{}, err
	}
	h, err := textField(fs, 4)
	if err != nil {
		return configuration{}, err
	}
	if id == 0 || !checksum(h) {
		return configuration{}, errors.New("missing full configuration identity")
	}
	tool, err := uintField(fs, 5)
	if err != nil || tool > 1 {
		return configuration{}, errors.New("invalid tool configuration flag")
	}
	return configuration{id, h, tool == 1}, nil
}

// DecodeCquery accepts exactly Bazel's --output=streamed_proto format. Its
// length-delimited records contain one ConfiguredTarget or Configuration each.
func DecodeCquery(data []byte) ([]Target, error) {
	if len(data) == 0 || len(data) > MaxProtoBytes {
		return nil, errors.New("cquery byte limit")
	}
	type pendingTarget struct {
		target    Target
		id        uint64
		legacy    string
		generator string
		inputIDs  []uint64
	}
	var pending []pendingTarget
	configs := make(map[uint64]string)
	tools := make(map[uint64]bool)
	edges := 0
	for len(data) > 0 {
		size, n := protowire.ConsumeVarint(data)
		if n < 0 || size == 0 || size > uint64(len(data)-n) {
			return nil, errors.New("invalid cquery record length")
		}
		fs, err := wireFields(data[n : n+int(size)])
		if err != nil {
			return nil, err
		}
		data = data[n+int(size):]
		if len(fs) != 1 || fs[0].t != protowire.BytesType {
			return nil, errors.New("noncanonical cquery record")
		}
		switch fs[0].n {
		case 2:
			c, err := decodeConfiguration(fs[0].b)
			if err != nil {
				return nil, err
			}
			if _, ok := configs[c.id]; ok || len(configs) >= MaxTargets {
				return nil, errors.New("duplicate or excessive configurations")
			}
			configs[c.id] = c.hash
			tools[c.id] = c.tool
		case 1:
			if len(pending) >= MaxTargets {
				return nil, errors.New("cquery target limit")
			}
			ct, err := wireFields(fs[0].b)
			if err != nil {
				return nil, err
			}
			id, err := uintField(ct, 3)
			if err != nil {
				return nil, err
			}
			legacyFields, err := message(ct, 2)
			if err != nil {
				return nil, err
			}
			legacy, err := textField(legacyFields, 4)
			if err != nil {
				return nil, err
			}
			tf, err := message(ct, 1)
			if err != nil {
				return nil, err
			}
			disc, err := uintField(tf, 1)
			if err != nil {
				return nil, err
			}
			if disc < 1 || disc > 5 {
				return nil, errors.New("unknown cquery target type")
			}
			rule, err := message(tf, protowire.Number(disc+1))
			if err != nil {
				return nil, err
			}
			name, err := textField(rule, 1)
			if err != nil || !label(name) {
				return nil, errors.New("invalid configured target label")
			}
			t := Target{Configured: Configured{Label: canonicalLabel(name)}, Kind: fmt.Sprintf("file:%d", disc), Inputs: []Configured{}, Units: []string{}}
			generator := ""
			var inputIDs []uint64
			if disc == 3 {
				generator, err = textField(rule, 2)
				if err != nil || !label(generator) {
					return nil, errors.New("generated file lacks its generating rule")
				}
				generator = canonicalLabel(generator)
			}
			if disc == 1 {
				t.Kind, err = textField(rule, 2)
				if err != nil || t.Kind == "" {
					return nil, errors.New("missing rule class")
				}
				seenInputs := map[string]bool{}
				for _, f := range rule {
					if f.n == 15 {
						if f.t != protowire.BytesType {
							return nil, errors.New("invalid configured edge")
						}
						in, err := wireFields(f.b)
						if err != nil {
							return nil, err
						}
						l, err := textField(in, 1)
						if err != nil || !label(l) {
							return nil, errors.New("invalid edge label")
						}
						h, err := textField(in, 2)
						if err != nil || (h != "" && !checksum(h)) {
							return nil, errors.New("invalid edge configuration")
						}
						c := Configured{canonicalLabel(l), h}
						inputID, err := uintField(in, 3)
						if err != nil {
							return nil, err
						}
						if seenInputs[c.key()] {
							return nil, errors.New("duplicate configured edge")
						}
						seenInputs[c.key()] = true
						t.Inputs = append(t.Inputs, c)
						inputIDs = append(inputIDs, inputID)
						edges++
						if edges > MaxEdges {
							return nil, errors.New("configured edge limit")
						}
					}
				}
			}
			pending = append(pending, pendingTarget{t, id, legacy, generator, inputIDs})
		default:
			return nil, errors.New("unknown cquery record")
		}
	}
	if len(pending) == 0 {
		return nil, errors.New("empty configured universe")
	}
	out := make([]Target, 0, len(pending))
	keys := map[string]bool{}
	for _, p := range pending {
		h := ""
		if p.id != 0 {
			var ok bool
			h, ok = configs[p.id]
			if !ok {
				return nil, errors.New("missing configuration record")
			}
		}
		if p.legacy != "" && p.legacy != h && (p.id != 0 || p.legacy != "null") {
			return nil, errors.New("configuration checksum disagreement")
		}
		p.target.Configuration = h
		for i, id := range p.inputIDs {
			if id != 0 && (configs[id] == "" || configs[id] != p.target.Inputs[i].Configuration) {
				return nil, errors.New("configured edge id/checksum disagreement")
			}
		}
		p.target.Tool = tools[p.id]
		if p.generator != "" {
			if h == "" {
				return nil, errors.New("generated file has no configuration")
			}
			p.target.Inputs = append(p.target.Inputs, Configured{p.generator, h})
			edges++
			if edges > MaxEdges {
				return nil, errors.New("configured edge limit")
			}
		}
		if keys[p.target.key()] {
			return nil, errors.New("duplicate configured target")
		}
		keys[p.target.key()] = true
		sort.Slice(p.target.Inputs, func(i, j int) bool { return p.target.Inputs[i].key() < p.target.Inputs[j].key() })
		out = append(out, p.target)
	}
	for _, t := range out {
		for _, in := range t.Inputs {
			if !keys[in.key()] {
				return nil, errors.New("configured edge leaves frozen universe")
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out, nil
}

type projectionLocator struct {
	Owner Configured
	Path  string
}

// ProjectionPaths lists only output artifacts of the pinned PhebsPlan aspect.
// Callers must read them relative to the private Bazel execution root.
func ProjectionPaths(data []byte) ([]string, error) {
	locs, err := decodeAquery(data)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(locs))
	for _, l := range locs {
		paths = append(paths, l.Path)
	}
	sort.Strings(paths)
	return paths, nil
}

func decodeAquery(data []byte) ([]projectionLocator, error) {
	if len(data) == 0 || len(data) > MaxProtoBytes {
		return nil, errors.New("aquery byte limit")
	}
	fs, err := wireFields(data)
	if err != nil {
		return nil, err
	}
	configs := map[uint64]string{}
	targets := map[uint64]string{}
	artifacts := map[uint64]uint64{}
	artifactTrees := map[uint64]bool{}
	aspects := map[uint64]string{}
	type fragment struct {
		label  string
		parent uint64
	}
	fragments := map[uint64]fragment{}
	var actions [][]wireField
	for _, f := range fs {
		if f.n != 1 && f.n != 2 && f.n != 3 && f.n != 5 && f.n != 6 && f.n != 8 {
			continue
		}
		if f.t != protowire.BytesType {
			return nil, errors.New("mistyped aquery record")
		}
		m, e := wireFields(f.b)
		if e != nil {
			return nil, e
		}
		id, e := uintField(m, 1)
		if e != nil {
			return nil, e
		}
		switch f.n {
		case 1:
			p, e := uintField(m, 2)
			if e != nil {
				return nil, e
			}
			if id == 0 || p == 0 || artifacts[id] != 0 || len(artifacts) >= MaxEdges {
				return nil, errors.New("invalid artifact id")
			}
			artifacts[id] = p
			tree, e := uintField(m, 3)
			if e != nil || tree > 1 {
				return nil, errors.New("invalid artifact type")
			}
			artifactTrees[id] = tree == 1
		case 2:
			if len(actions) >= MaxProjections {
				return nil, errors.New("projection action limit")
			}
			actions = append(actions, m)
		case 3:
			l, e := textField(m, 2)
			if e != nil || !label(l) {
				return nil, errors.New("invalid action target")
			}
			if id == 0 || targets[id] != "" || len(targets) >= MaxTargets {
				return nil, errors.New("duplicate action target")
			}
			targets[id] = canonicalLabel(l)
		case 5:
			c, e := decodeConfiguration(f.b)
			if e != nil {
				return nil, e
			}
			if configs[c.id] != "" || len(configs) >= MaxTargets {
				return nil, errors.New("duplicate action configuration")
			}
			configs[c.id] = c.hash
		case 6:
			name, e := textField(m, 2)
			if e != nil || id == 0 || name == "" || aspects[id] != "" || len(aspects) >= MaxProjections {
				return nil, errors.New("invalid aspect identity")
			}
			for _, field := range m {
				if field.n == 3 {
					return nil, errors.New("unexpected aspect parameters")
				}
			}
			aspects[id] = name
		case 8:
			l, e := textField(m, 2)
			if e != nil {
				return nil, e
			}
			p, e := uintField(m, 3)
			if e != nil {
				return nil, e
			}
			if id == 0 || l == "" || strings.ContainsAny(l, "/\\") || l == "." || l == ".." || len(fragments) >= MaxEdges {
				return nil, errors.New("invalid path fragment")
			}
			if _, ok := fragments[id]; ok {
				return nil, errors.New("duplicate path fragment")
			}
			fragments[id] = fragment{l, p}
		}
	}
	var out []projectionLocator
	seen := map[string]bool{}
	for _, m := range actions {
		mn, e := textField(m, 4)
		if e != nil || mn != "PhebsPlan" {
			return nil, errors.New("unexpected action mnemonic")
		}
		t, e := uintField(m, 1)
		if e != nil {
			return nil, e
		}
		c, e := uintField(m, 5)
		if e != nil {
			return nil, e
		}
		if targets[t] == "" || configs[c] == "" {
			return nil, errors.New("unbound action owner")
		}
		var aspectIDs []uint64
		for _, f := range m {
			if f.n != 2 {
				continue
			}
			switch f.t {
			case protowire.VarintType:
				aspectIDs = append(aspectIDs, f.v)
			case protowire.BytesType:
				b := f.b
				for len(b) > 0 {
					v, n := protowire.ConsumeVarint(b)
					if n < 0 {
						return nil, errors.New("invalid packed aspect ids")
					}
					aspectIDs = append(aspectIDs, v)
					b = b[n:]
					if len(aspectIDs) > 1 {
						return nil, errors.New("unexpected action aspect chain")
					}
				}
			default:
				return nil, errors.New("invalid aspect id type")
			}
		}
		if len(aspectIDs) != 1 || canonicalLabel(aspects[aspectIDs[0]]) != "@@//phebs_plan:aspect.bzl%phebs_plan" {
			return nil, errors.New("action was not generated by the pinned planner aspect")
		}
		var outputs []uint64
		for _, f := range m {
			if f.n == 9 {
				switch f.t {
				case protowire.VarintType:
					outputs = append(outputs, f.v)
				case protowire.BytesType:
					b := f.b
					for len(b) > 0 {
						v, n := protowire.ConsumeVarint(b)
						if n < 0 {
							return nil, errors.New("bad packed output ids")
						}
						outputs = append(outputs, v)
						b = b[n:]
						if len(outputs) > 1 {
							return nil, errors.New("multiple projection outputs")
						}
					}
				default:
					return nil, errors.New("bad output ids")
				}
			}
		}
		if len(outputs) != 1 || artifacts[outputs[0]] == 0 || artifactTrees[outputs[0]] {
			return nil, errors.New("missing projection output")
		}
		var parts []string
		at := artifacts[outputs[0]]
		visited := map[uint64]bool{}
		for at != 0 {
			f, ok := fragments[at]
			if !ok || visited[at] || len(parts) >= 128 {
				return nil, errors.New("invalid output path graph")
			}
			visited[at] = true
			parts = append(parts, f.label)
			at = f.parent
		}
		for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
		p := strings.Join(parts, "/")
		if !relative(p) || !strings.HasPrefix(p, "bazel-out/") || !strings.HasSuffix(p, ".phebs-plan.json") || seen[p] {
			return nil, errors.New("ambiguous projection path")
		}
		seen[p] = true
		out = append(out, projectionLocator{Configured{targets[t], configs[c]}, p})
	}
	if len(out) == 0 {
		return nil, errors.New("empty projection action set")
	}
	return out, nil
}
