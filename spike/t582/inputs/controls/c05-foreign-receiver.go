// Authored Apache-2.0 T58.2 source-only control; never execute.
package probe
import "context"
type ordinary struct{}
func (*ordinary) EmitBatch(context.Context,any) error { return nil }
func probe(o *ordinary, ctx context.Context) { _ = o.EmitBatch(ctx,nil) }
