package provider

import (
	"context"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func generatedBytes(ctx context.Context, observations []DocumentOutcome, p planner.Plan, read func(string, int64) ([]byte, error)) (map[string][]byte, error) {
	docs := make(map[string]planner.Document, len(p.Documents))
	for _, d := range p.Documents {
		docs[d.ID] = d
	}
	result := map[string][]byte{}
	var total int64
	for _, o := range observations {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		d := docs[o.Document]
		if d.Kind != "generated" || o.State != "included" {
			continue
		}
		if o.Path == "" || !typedindex.IsGeneratedPath(o.Path) || result[o.Path] != nil || d.Bytes < 0 || d.Bytes > typedindex.MaxGeneratedBytes || int64(d.Bytes) > typedindex.MaxGeneratedAggregateBytes-total {
			return nil, typedindex.Invalid
		}
		b, e := read(launcher.ExecRoot+"/"+d.ExecPath, int64(d.Bytes))
		if e != nil || len(b) != d.Bytes || hash(b) != "sha256:"+d.SHA256 {
			return nil, typedindex.Stale
		}
		total += int64(len(b))
		result[o.Path] = b
	}
	return result, nil
}
