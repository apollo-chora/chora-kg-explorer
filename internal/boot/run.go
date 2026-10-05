package boot

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/tracing"
)

// Process-level collaborators, as package variables so a unit test can prove
// the boot composition (which identities reach the subscriber) without ADC, a
// network dial or a broker. Production never reassigns them.
var (
	initTracing     = tracing.Init
	newGatewayLLM   = NewGatewayLLM
	serveSubscriber = agentdispatch.RunSubscriberOnly
)

// Run boots and serves kg_explorer subscriber-only (ADR-254 D6). It returns
// rather than exits; a non-nil error means the pod must die.
func Run(ctx context.Context, args []string) error {
	if err := refuseArgs(args); err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	traceShutdown, err := initTracing(ctx, CrewKind)
	if err != nil {
		return fmt.Errorf("tracing.Init: %w", err)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			slog.Error("trace shutdown error", "err", err)
		}
	}()
	slog.Info(CrewKind+" boot", cfg.LogAttrs()...)

	llm, err := newGatewayLLM(ctx, cfg)
	if err != nil {
		return err
	}
	root, err := NewAgent(cfg, llm)
	if err != nil {
		return err
	}
	plugins, err := NewPlugins()
	if err != nil {
		return err
	}
	return serveSubscriber(ctx, dispatchServeConfig(cfg, root, plugins), agentdispatch.RunOptions{})
}

// refuseArgs rejects any command-line argument: this binary was born
// subscriber-only (ADR-254 D6); a container command that carries the fleet's
// old "web -port ..." launcher line dies with the cause in its exit line.
func refuseArgs(args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("%s takes no arguments, got %q: the binary is subscriber-only "+
		"(ADR-254 D6); update the Deployment command", CrewKind, args)
}

// dispatchServeConfig is the lane identity: role kg_explore, Deployment
// chora-kg-explorer, stateless per-dispatch sessions.
func dispatchServeConfig(cfg Config, root agent.Agent, plugins []*plugin.Plugin) agentdispatch.ServeConfig {
	return agentdispatch.ServeConfig{
		AgentRole:   DispatchRole,
		ServiceName: ServiceName,
		AppName:     cfg.AgentAppName,
		RootAgent:   root,
		Sessions:    session.InMemoryService(),
		Plugins:     runner.PluginConfig{Plugins: plugins},
	}
}

// gatewayConfig is the gateway client configuration: the crew kind is the
// agent id (ADR-254 D7), the surface is the crew id. Pure, so it is assertable.
func gatewayConfig(cfg Config) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:         cfg.GatewayEndpoint,
		LogicalModelID:   cfg.Model,
		FallbackModelIDs: cfg.FallbackModels,
		AgentID:          CrewKind,
		CrewKind:         CrewSurface,
		Surface:          CrewSurface,
		TenantID:         cfg.GatewayTenantID,
		GCID:             cfg.GatewayGCID,
		Audience:         cfg.GatewayAudience,
	}
}

// NewGatewayLLM builds the chora-model-gateway-fronted adkmodel.LLM (ADR-163):
// the single un-bypassable LLM chokepoint.
func NewGatewayLLM(ctx context.Context, cfg Config) (adkmodel.LLM, error) {
	m, err := modelgatewayclient.New(ctx, gatewayConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.New(%s, %s @ %s): %w", CrewKind, cfg.Model, cfg.GatewayEndpoint, err)
	}
	return m, nil
}
