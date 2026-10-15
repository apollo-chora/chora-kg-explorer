package agentconfig_test

import (
	"testing"

	"github.com/apollo-chora/chora-kg-explorer/internal/agentconfig"
)

func TestKgExplorerStandardTier(t *testing.T) {
	cfg, err := agentconfig.KgExplorer()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != "kg_explorer" {
		t.Fatalf("agent = %q", cfg.Agent)
	}
	sub, err := cfg.Sub("explorer")
	if err != nil {
		t.Fatal(err)
	}
	// The model id is env-only (KG_EXPLORER_MODEL); the YAML declares only the
	// tier and the prompt version.
	if sub.Tier != "standard" || sub.PromptVersion != "v2" {
		t.Fatalf("explorer config = %+v", sub)
	}
}

func TestSubMissingFailsLoud(t *testing.T) {
	cfg, err := agentconfig.KgExplorer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Sub("nonexistent"); err == nil {
		t.Fatal("a missing sub-agent must fail loud")
	}
}
