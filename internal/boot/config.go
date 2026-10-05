// Package boot holds the resolved boot wiring of kg_explorer, the Knowledge
// Graph Explorer agent (ADR-254 D2 / R23): the ADR-227 reveal model call that
// used to live in chora-fog-orchestrator, now a subscriber-only agent on
// dispatch role kg_explore (ADR-254 D6). Same split as the diagnosis crew:
// config.go resolves env + embedded agentconfig into a Config and returns
// errors instead of exiting; agents.go builds the agent with the LLM injected;
// plugins.go pins the plugin chain; run.go is the thin glue that needs ADC,
// the network and the blocking receive loop.
//
// No agent-side mana gate and no tenancy client: the gateway is the single
// meter and screen (ADR-177 / ADR-254 A3, coordinator ruling 2026-08-22).
package boot

import (
	"errors"
	"fmt"
	"os"

	"github.com/apollo-chora/chora-kg-explorer/internal/agentconfig"
)

// Identity: the ADK agent name, the termination AgentID and the gateway
// agent_id (ADR-254 D7) are the crew kind; CrewSurface is the crew id stamped
// as InvokeRequest.surface on every model call.
const (
	CrewKind    = "kg_explorer"
	CrewSurface = "kg_exploration"
)

// envModel lets ops override the primary model for a quick experiment; tier,
// fallback chain and prompt version stay YAML-declared.
const envModel = "KG_EXPLORER_MODEL"

// Config is the binary's fully resolved boot configuration.
type Config struct {
	ProjectID    string
	AgentAppName string

	GatewayEndpoint string
	// GatewayAudience is the ID-token audience claim minted for the gateway
	// call. Read here rather than left to modelgatewayclient's internal
	// default, which lives in TWO places (client.go and image.go) and which
	// nothing could previously state or override. The default below is the
	// value the library already used, so this is not a behaviour change.
	GatewayAudience string
	GatewayTenantID string
	GatewayGCID     string

	Model          string
	FallbackModels []string
	PromptVersion  string

	Env string
}

// EnvOr returns os.Getenv(name) if non-empty, else fallback.
func EnvOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// LoadConfig resolves the kg_explorer boot config.
func LoadConfig() (Config, error) {
	cfg, err := agentconfig.KgExplorer()
	if err != nil {
		return Config{}, fmt.Errorf("%s: load agent config: %w", CrewKind, err)
	}
	sub, err := cfg.Sub("explorer")
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", CrewKind, err)
	}
	projectID := os.Getenv("CHORA_PROJECT_ID")
	if projectID == "" {
		return Config{}, errors.New(CrewKind + ": CHORA_PROJECT_ID required")
	}
	gatewayTenantID := os.Getenv("CHORA_GATEWAY_TENANT_ID")
	gatewayGCID := os.Getenv("CHORA_GATEWAY_GCID")
	if gatewayTenantID == "" || gatewayGCID == "" {
		return Config{}, fmt.Errorf("%s: CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required "+
			"(ADR-163; no in-memory fallback per feedback_no_stubs_real_wiring)", CrewKind)
	}
	return Config{
		ProjectID:       projectID,
		AgentAppName:    os.Getenv("CHORA_AGENT_APP_NAME"),
		GatewayEndpoint: EnvOr("CHORA_GATEWAY_ENDPOINT", "gateway.chora.site:443"),
		GatewayAudience: EnvOr("CHORA_GATEWAY_AUDIENCE", "https://gateway.chora.site"),
		GatewayTenantID: gatewayTenantID,
		GatewayGCID:     gatewayGCID,
		Model:           EnvOr(envModel, sub.PrimaryModel),
		FallbackModels:  sub.FallbackModels,
		PromptVersion:   sub.PromptVersion,
		Env:             EnvOr("CHORA_ENV", "dev"),
	}, nil
}

// LogAttrs renders the boot line's slog key/value pairs (GCID truncated).
func (c Config) LogAttrs() []any {
	gcid := c.GatewayGCID
	if len(gcid) > 8 {
		gcid = gcid[:8] + "..."
	}
	return []any{
		"project", c.ProjectID, "agent_app_name", c.AgentAppName,
		"model", c.Model, "fallback", c.FallbackModels, "prompt_version", c.PromptVersion,
		"gateway_endpoint", c.GatewayEndpoint, "gateway_tenant_id", c.GatewayTenantID, "gateway_gcid_prefix", gcid,
		"chora_env", c.Env, "surface", CrewSurface,
	}
}
