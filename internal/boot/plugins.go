package boot

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/terminationplugin"
	"github.com/apollo-chora/chora-adk-common/tracing"

	kagent "github.com/apollo-chora/chora-kg-explorer/internal/agent"
)

// Pinned termination identity (D6 P3 trace-emission contract). The explorer
// is single-shot: one model call per dispatch, two covers one retry.
const (
	terminationRuntime       = "AGENT_EXECUTION_RUNTIME_ADK_GO"
	terminationCrewPattern   = "P1_SINGLE_AGENT"
	terminationMaxIterations = 2
)

// StateString reads a string value from ADK session state ("" on miss).
func StateString(state interface {
	Get(string) (any, error)
}, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// NewInboundTracePlugin links the agent's local trace to the dispatch
// envelope's W3C traceparent (set in session state by agentdispatch).
func NewInboundTracePlugin() (*plugin.Plugin, error) {
	p, err := plugin.New(plugin.Config{
		Name: "chora_inbound_trace_" + CrewKind,
		BeforeAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			traceparent := StateString(ctx.State(), "traceparent")
			if traceparent == "" {
				return nil, nil
			}
			tracing.AddInboundLink(ctx, traceparent, StateString(ctx.State(), "tracestate"))
			return nil, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("inbound-trace plugin.New: %w", err)
	}
	return p, nil
}

// TerminationConfig is the pinned termination-plugin configuration: AgentID
// is the binary's agent name, CrewKind the crew id.
func TerminationConfig() terminationplugin.Config {
	return terminationplugin.Config{
		Publisher:     &terminationplugin.LoggingPublisher{},
		AgentID:       CrewKind,
		Runtime:       terminationRuntime,
		CrewKind:      CrewSurface,
		CrewPattern:   terminationCrewPattern,
		MaxIterations: terminationMaxIterations,
	}
}

// ActionCode resolves the gateway action code from the payload's
// request_source (ADR-254 D7): a learner's own request is metered as
// knowledge_graph_traverse; a campaign free reveal and an atom refresh are
// un-metered. An unknown source resolves to "" here and is refused by name,
// permanently, by the agent before any model call.
func ActionCode(get func(key string) string) string {
	code, err := kagent.ActionCodeFor(get(stateKeyRequestSource))
	if err != nil {
		return ""
	}
	return code
}

// NewPlugins builds the plugin chain in runtime order:
// [inboundTrace, tenantProp (per-request tenant/gcid + action code), termination].
// No mana or tenancy plugin: the gateway meters (ADR-177 / ADR-254 A3).
func NewPlugins() ([]*plugin.Plugin, error) {
	inboundTraceP, err := NewInboundTracePlugin()
	if err != nil {
		return nil, err
	}
	tenantPropP, err := modelgatewayclient.NewTenantPropagationPlugin(CrewKind,
		modelgatewayclient.WithActionCodeResolver(ActionCode))
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.NewTenantPropagationPlugin: %w", err)
	}
	terminationP, err := terminationplugin.New(TerminationConfig())
	if err != nil {
		return nil, fmt.Errorf("terminationplugin.New: %w", err)
	}
	return []*plugin.Plugin{inboundTraceP, tenantPropP, terminationP}, nil
}
