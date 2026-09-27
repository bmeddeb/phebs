package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// resultDimensions visits tokens before any typed slice/map allocation. String
// tokens can still allocate up to the enclosing bounded worker byte allowance.
// No permissive object map or generic exported schema machinery is introduced.
func resultDimensions(ctx context.Context, raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	rawEdges, planEdges := 0, 0
	var value func(string, int) error
	value = func(at string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 24 {
			return typedindex.Capacity
		}
		t, err := d.Token()
		if err != nil {
			return typedindex.Invalid
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			limit := 64
			if at == "generated" {
				limit = typedindex.MaxBundleDocuments
			}
			if strings.HasSuffix(at, "/imports") {
				limit = planner.MaxUnits
			}
			keys := map[string]bool{}
			for d.More() {
				if len(keys) >= limit {
					return typedindex.Capacity
				}
				t, err := d.Token()
				key, ok := t.(string)
				if err != nil || !ok || keys[key] || len(key) > 4096 {
					return typedindex.Invalid
				}
				keys[key] = true
				next := strings.ToLower(key)
				if at != "" {
					next = at + "/" + strings.ToLower(key)
				}
				if err = value(next, depth+1); err != nil {
					return err
				}
			}
		case '[':
			limit := resultArrayLimit(at)
			for n := 0; d.More(); n++ {
				if n >= limit {
					return typedindex.Capacity
				}
				switch at {
				case "raw_plan/targets/*/inputs", "raw_plan/targets/*/units", "raw_plan/units/*/imports":
					rawEdges++
					if rawEdges > typedindex.MaxBundleEdges {
						return typedindex.Capacity
					}
				case "plan/targets/*/dependencies", "plan/targets/*/units", "plan/units/*/imports", "plan/units/*/documents":
					planEdges++
					if planEdges > typedindex.MaxBundleEdges {
						return typedindex.Capacity
					}
				}
				if err = value(at+"/*", depth+1); err != nil {
					return err
				}
			}
		default:
			return typedindex.Invalid
		}
		end, err := d.Token()
		if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
			return typedindex.Invalid
		}
		return nil
	}
	if err := value("", 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return typedindex.Invalid
	}
	return nil
}

func resultArrayLimit(at string) int {
	last := at[strings.LastIndex(at, "/")+1:]
	switch strings.ReplaceAll(strings.ToLower(last), "_", "") {
	case "legs":
		return 2
	case "targets":
		return typedindex.MaxBundleTargets
	case "units":
		return typedindex.MaxBundleUnits
	case "sdks":
		return 64
	case "documents":
		if at == "plan/documents" {
			return typedindex.MaxBundleDocuments
		}
		return planner.MaxDocuments
	case "packages":
		if strings.HasPrefix(at, "raw_plan/sdks/") {
			return planner.MaxSDKPackages
		}
		return planner.MaxUnits
	case "imports", "inputs", "dependencies":
		return planner.MaxEdges
	case "gofiles", "compiledgofiles", "otherfiles", "sourceimports", "sources", "caches":
		return planner.MaxDocuments
	case "exports":
		return planner.MaxUnits
	case "argv", "clientargv", "environment", "arguments", "patterns":
		return 256
	case "uid", "gid":
		return 4
	default:
		return 64
	}
}
