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
	if sub.Tier != "standard" || sub.PrimaryModel != "longcat-2.5-preview" || sub.PromptVersion != "v2" {
		t.Fatalf("explorer config = %+v", sub)
	}
	if len(sub.FallbackModels) != 1 || sub.FallbackModels[0] != "longcat-2.5-preview" {
		t.Fatalf("fallback chain = %v", sub.FallbackModels)
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
