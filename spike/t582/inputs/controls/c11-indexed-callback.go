// Authored Apache-2.0 T58.2 source-only control; never execute.
package probe
import ("context"; "github.com/uber/jaeger-client-go/thrift-gen/jaeger"; agent "github.com/uber/jaeger-client-go/thrift-gen/agent")
func probe(c *agent.AgentClient, ctx context.Context, index int) {
 callbacks := []func(context.Context,*jaeger.Batch) error{c.EmitBatch}
 _ = callbacks[index](ctx,nil)
}
