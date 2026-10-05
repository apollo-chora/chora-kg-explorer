// Command kg_explorer is the Knowledge Graph Explorer agent (ADR-254 D2 /
// R23): the ADR-227 reveal model call that used to live in
// chora-fog-orchestrator, now a subscriber-only agent (ADR-254 D6) on dispatch
// role kg_explore. It proposes up to six concepts (with atom_refs drawn ONLY
// from the entitled catalogue in the payload) and up to six typed edges
// between the learner's EXISTING concepts; the learner owns the map and
// accepts or dismisses. No ADK web launcher, a health port (/healthz, /readyz)
// and nothing else, a subscriber that cannot start ends the process non-zero,
// and the binary takes no arguments.
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	CHORA_PROJECT_ID                       required; stamped on the boot log line
//	CHORA_AGENT_APP_NAME                   ADK session AppName label (optional)
//	KG_EXPLORER_MODEL                      primary model override (default: embedded agentconfig YAML)
//	CHORA_GATEWAY_ENDPOINT                 default gateway.chora.site:443
//	CHORA_GATEWAY_TENANT_ID / _GCID        required (ADR-163; per-request values from the dispatch win)
//	CHORA_ENV                              dev | staging | prod
//	AGENT_DISPATCH_ENABLED                 must be "true" (no other transport)
//	AGENT_DISPATCH_SUBSCRIPTION            request subscription (chora-kg-explorer.agent-dispatch-kg-explore-requested)
//	NATS_URL                               NATS JetStream event bus of the dispatch lanes
//	AGENT_HEALTH_PORT                      health port (default 8080)
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/apollo-chora/chora-kg-explorer/internal/boot"
)

// Build stamps (injected via -ldflags in the Dockerfile; "unknown" locally).
var (
	serviceName = "chora-kg-explorer"
	gitSHA      = "unknown"
	buildTime   = "unknown"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	// SIGTERM (rollout, scale-down) cancels the context: the dispatch
	// subscriber stops receiving, in-flight work finishes, and the process
	// exits 0. Any other way out is an error and exits non-zero (ADR-254 D6).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := boot.Run(ctx, os.Args[1:]); err != nil {
		log.Fatalf("kg_explorer: %v", err)
	}
}
