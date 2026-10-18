// Authored Apache-2.0 T58.2 source-only control; never execute.
package probe
import ("context"; agent "github.com/uber/jaeger-client-go/thrift-gen/agent")
func wrap(h agent.Agent) any { return agent.NewAgentProcessor(h) }
func probe(h agent.Agent) { _ = wrap(h) }
