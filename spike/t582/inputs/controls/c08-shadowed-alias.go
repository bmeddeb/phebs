// Authored Apache-2.0 T58.2 source-only control; never execute.
package probe
import agent "github.com/uber/jaeger-client-go/thrift-gen/agent"
var _ agent.Agent
type localFactory struct{}
func (localFactory) NewAgentProcessor(any) any { return nil }
func probe() {
 agent := localFactory{}
 _ = agent.NewAgentProcessor(nil)
}
