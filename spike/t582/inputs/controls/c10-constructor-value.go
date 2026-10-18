// Authored Apache-2.0 T58.2 source-only control; never execute.
package probe
import ("context"; agent "github.com/uber/jaeger-client-go/thrift-gen/agent")
func probe(h agent.Agent) {
 ctor := agent.NewAgentProcessor
 _ = ctor(h)
}
