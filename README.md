# chora-kg-explorer

## About

chora-kg-explorer is a Go service that consumes Chora agent-dispatch requests for the `kg_explore` role and produces concept and edge proposals for a learner's knowledge graph. It runs as a subscriber-only agent on NATS JetStream, calls `chora-model-gateway` for the model invocation, and exposes only health and readiness endpoints over HTTP. Model and prompt configuration is embedded in the binary, with environment variables for deployment-specific settings.

## Quick start

### Prerequisites

- Go 1.26.6
- A running NATS JetStream server
- A reachable Chora model gateway
- `CHORA_PROJECT_ID`, `CHORA_GATEWAY_TENANT_ID`, and `CHORA_GATEWAY_GCID`

Clone and build:

```sh
git clone https://github.com/apollo-chora/chora-kg-explorer.git
cd chora-kg-explorer
go build ./...
```

For local development, set the required configuration and enable the dispatch subscriber:

```sh
export CHORA_PROJECT_ID=local
export CHORA_GATEWAY_TENANT_ID=your-tenant-id
export CHORA_GATEWAY_GCID=your-gcid
export NATS_URL=nats://localhost:4222
export AGENT_DISPATCH_ENABLED=true
go run ./cmd/kg_explorer
```

The service listens for health traffic on port `8080` by default.

## Usage

The binary is subscriber-only and takes no command-line arguments:

```sh
go run ./cmd/kg_explorer
```

It consumes the `kg_explore` dispatch lane over NATS JetStream. The request payload contains:

- `exploration_json`: a JSON object containing the focal concept, existing concepts, the atom catalogue, optional map theme and learner-focus data, and `request_source`.
- `request_source`: `learner_request`, `campaign_free_reveal`, or `atom_refresh`. An unknown value is rejected before a model call.

The model returns up to six new concept proposals and up to six edges between existing concept IDs. Atom references are accepted only when the atom ID exists in the supplied catalogue; edges are accepted only when both concept IDs exist, the edge is not a self-loop, and the class is `hierarchy` or `lateral`. A non-JSON model response is treated as an empty proposal set.

The completion envelope is JSON with `concepts`, `edges`, `model_id`, `prompt_version`, and `prompt_source`.

Health endpoints:

- `GET /healthz`
- `GET /readyz`

Configuration:

| Variable | Purpose | Default |
| --- | --- | --- |
| `CHORA_PROJECT_ID` | Project identity used at boot | Required |
| `CHORA_AGENT_APP_NAME` | ADK session application name | Unset |
| `KG_EXPLORER_MODEL` | Overrides the configured primary model | `longcat-2.5-preview` |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway address | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` | Gateway tenant scope | Required |
| `CHORA_GATEWAY_GCID` | Gateway GCID scope | Required |
| `CHORA_GATEWAY_AUDIENCE` | ID-token audience for the gateway | `https://gateway.chora.site` |
| `CHORA_ENV` | Environment label | `dev` |
| `NATS_URL` | NATS JetStream endpoint | Required |
| `AGENT_DISPATCH_ENABLED` | Enables the dispatch subscriber | Must be `true` |
| `AGENT_DISPATCH_SUBSCRIPTION` | Overrides the request consumer name | `chora-kg-explorer.agent-dispatch-kg-explore-requested` |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | Redelivery limit | `5` |
| `AGENT_HEALTH_PORT` | HTTP health port | `8080` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | Stdout when unset |
| `CHORA_SERVICE_VERSION` | OTLP `service.version` value | `dev` |

The embedded agent configuration in `internal/agentconfig/kg_explorer.yaml` selects `longcat-2.5-preview` as both the primary model and the fallback, with prompt version `v2`. Setting `KG_EXPLORER_MODEL` changes only the primary model; the fallback list and prompt version remain configuration-defined.

For deployment, `Dockerfile` builds the service from the repository root and produces an image containing the `/usr/local/bin/kg_explorer` binary.

## Development

The repository is organized as follows:

| Path | Purpose |
| --- | --- |
| `cmd/kg_explorer/` | Service entry point |
| `internal/agent/` | Prompt composition, request parsing, and fail-closed output parsing |
| `internal/agentconfig/` | Embedded model and prompt configuration |
| `internal/boot/` | Configuration, gateway wiring, plugins, dispatch setup, and process startup |

Run the same checks used by CI:

```sh
gofmt -l .
go mod tidy
git diff --exit-code -- go.mod go.sum
go vet ./...
go test ./...
```

The test suite covers configuration defaults and required variables, dispatch and gateway identity, plugin composition, request validation, prompt construction, fail-closed proposal parsing, and the subscriber boot path. Tests use in-memory/session and scripted components, so they do not require a broker, database, or external network service.

The project uses Go modules. Shared Chora dependencies are resolved through the Go module system; the repository does not require a sibling checkout or Go workspace.
