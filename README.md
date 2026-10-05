# chora-kg-explorer

The Knowledge Graph Explorer ADK Go agent — crew `kg_exploration`, dispatch role
`kg_explore` (ADR-254 D2 / R23): the ADR-227 reveal model call that used to live
in `chora-fog-orchestrator`, now a subscriber-only agent (ADR-254 D6) on the
NATS JetStream dispatch lane `kg_explore`. One binary, `cmd/kg_explorer` (no
arguments; health port `/healthz` + `/readyz` only).

Module path: `github.com/apollo-chora/chora-kg-explorer`.

The crew is cloud-neutral: NATS JetStream for events (via
`chora-adk-common/agentdispatch`), standard OTLP for traces (via
`chora-adk-common/tracing`), env-backed secrets, and the model-gateway gRPC
adapter for model calls (via `chora-adk-common/modelgatewayclient`). No cloud
account or managed service is required.

## Behaviour

- **Payload** (`input_payload`): `exploration_json` (focal title, focal atom
  refs, existing concepts `{concept_id, title}`, map theme, banded atom
  catalogue, sub-goal, goal title, ancestors, weakness) +
  `request_source in {learner_request, campaign_free_reveal, atom_refresh}`
  (anything else is a permanent `FAILED unknown_request_source` before any
  model call).
- **Completion envelope**: `{concepts[{title, rationale, atom_refs}],
  edges[{source_concept_id, target_concept_id, edge_class, rationale}],
  model_id, prompt_version, prompt_source}`. Proposals are fail-closed:
  atom_refs only from the payload catalogue, edges only between existing
  concept ids, hierarchy|lateral, deduped, soft caps 6 + 6; a non-JSON answer
  is zero proposals, never an error. The learner owns the map (ADR-212/214).
- **Gateway**: surface `kg_exploration`, agent id `kg_explorer`, the dispatch
  idempotency key on every Invoke; action code per D7
  (`knowledge_graph_traverse` for a learner request, `""` for a free reveal /
  atom refresh). No agent-side mana or tenancy plugin: the gateway meters.
- **Model**: `internal/agentconfig/kg_explorer.yaml` (gemini-2.5-flash,
  fallback flash-lite, prompt v2); `KG_EXPLORER_MODEL` overrides the primary
  only.

## Layout

| Path | Purpose |
|---|---|
| `cmd/kg_explorer/` | The subscriber-only binary (no arguments). |
| `internal/boot/` | Boot wiring: env + embedded agentconfig resolution, agent tree, plugin chain, subscriber serve. |
| `internal/agent/` | Pure prompt composition and fail-closed output parsing. |
| `internal/agentconfig/` | Embedded per-agent model + prompt YAML. |

## Transport and dependencies

- **Events** — NATS JetStream via `chora-common/eventbus` (inside
  `chora-adk-common/agentdispatch`). The dispatch subjects
  (`chora.ai_kernel.agent_dispatch.kg_explore_{requested,completed}.v1`) are
  valid NATS subjects; the canonical event envelope rides as NATS headers.
- **Traces** — standard OTLP/gRPC via `chora-common/otel`; stdout in local dev
  when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset.
- **Model calls** — gRPC to `chora-model-gateway`; TLS + `CHORA_GATEWAY_TOKEN`
  in production, plaintext when `CHORA_GATEWAY_INSECURE` is set for local dev.

The service listens on the health port `AGENT_HEALTH_PORT` (default `8080`)
and needs NATS plus the model gateway — no database.

## Configuration

| Variable | Purpose | Local default |
| --- | --- | --- |
| `CHORA_PROJECT_ID` | Project identity stamped on the boot log line | unset (required) |
| `CHORA_AGENT_APP_NAME` | ADK session AppName label | unset |
| `KG_EXPLORER_MODEL` | Primary model override (tier/fallback/prompt stay YAML-declared) | embedded YAML |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway address | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` / `CHORA_GATEWAY_GCID` | Gateway tenant scope (ADR-163) | unset (required) |
| `CHORA_GATEWAY_AUDIENCE` | ID-token audience for the gateway call | `https://gateway.chora.site` |
| `CHORA_ENV` | Environment label (`dev` \| `staging` \| `prod`) | `dev` |
| `NATS_URL` | NATS JetStream event bus (dispatch subscriber) | unset (required) |
| `AGENT_DISPATCH_ENABLED` | Opt in to the dispatch subscriber (must be `true`) | `false` |
| `AGENT_DISPATCH_SUBSCRIPTION` | Consumer name override | `chora-kg-explorer.agent-dispatch-kg-explore-requested` |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | Redelivery ceiling (must match the consumer) | `5` |
| `AGENT_HEALTH_PORT` | Health/readiness port | `8080` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | Stamped as the OTLP `service.version` attribute | `dev` |

## Build and test

```sh
go build ./...
go vet ./...
gofmt -l .   # must be empty
go test ./...
```

The suite is hermetic — no broker, database, or network is required.

See `Dockerfile` for the container build (build context = this repository).
