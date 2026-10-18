// Authored Apache-2.0 T58.2 source-only control; never execute.
package probe
import ("context"; "github.com/uber/jaeger-client-go/thrift-gen/jaeger"; agent "github.com/uber/jaeger-client-go/thrift-gen/agent")
type handler struct{}
func (*handler) EmitBatch(context.Context,*jaeger.Batch) error { return nil }
func probe(h *handler,ctx context.Context) { _ = h.EmitBatch(ctx,nil) }
