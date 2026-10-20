// Package agentconfig holds the build-time tier + prompt configuration of the
// kg_explorer agent (ADR-254 D2 / R23). One embedded YAML declares tier /
// prompt_version; the model itself is env-only. Boot reads the REQUIRED
// KG_EXPLORER_MODEL env var and sends it as the chora-model-gateway
// logical_model_id, with the optional KG_EXPLORER_FALLBACK_MODELS comma list as
// the declared fallback chain. Mana is a quota, never a model selector, so the
// tier stays config-declared here while no literal model name is compiled in.
package agentconfig

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed kg_explorer.yaml
var kgExplorerYAML []byte

// SubAgentConfig is one sub-agent's resolved model tier + prompt selection.
type SubAgentConfig struct {
	Tier          string `yaml:"tier"`
	PromptVersion string `yaml:"prompt_version"`
}

// AgentConfig is the agent binary's full per-sub-agent config.
type AgentConfig struct {
	Agent     string                    `yaml:"agent"`
	SubAgents map[string]SubAgentConfig `yaml:"sub_agents"`
}

// Sub returns the named sub-agent config, failing loud when the YAML omits it
// or leaves the prompt version empty: a config gap is a build error, never a
// silent default. The model id is not declared here — boot reads it from the
// REQUIRED KG_EXPLORER_MODEL env var.
func (c AgentConfig) Sub(name string) (SubAgentConfig, error) {
	sc, ok := c.SubAgents[name]
	if !ok {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q has no sub-agent %q", c.Agent, name)
	}
	if sc.PromptVersion == "" {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q sub-agent %q has empty prompt_version", c.Agent, name)
	}
	return sc, nil
}

// KgExplorer parses the embedded kg_explorer config.
func KgExplorer() (AgentConfig, error) { return parse(kgExplorerYAML) }

func parse(raw []byte) (AgentConfig, error) {
	var c AgentConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return AgentConfig{}, fmt.Errorf("agentconfig: unmarshal: %w", err)
	}
	if c.Agent == "" {
		return AgentConfig{}, fmt.Errorf("agentconfig: missing top-level agent name")
	}
	if len(c.SubAgents) == 0 {
		return AgentConfig{}, fmt.Errorf("agentconfig: agent %q declares no sub_agents", c.Agent)
	}
	return c, nil
}
