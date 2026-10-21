# chora-kg-explorer

> A subscriber-only agent for the `kg_explore` role: given a learner's focal concept and
> the surrounding map, it proposes new concepts and edges for the knowledge graph.

Go, one binary, no database. It consumes agent-dispatch requests over NATS JetStream,
calls the platform's model gateway, and exposes nothing but health over HTTP.

## What it is

One of the single-agent lanes the AI kernel dispatches to. When a learner asks to expand
their map — or a campaign reveals a concept, or an atom refresh triggers it — the kernel
publishes an `exploration_json` payload and this agent turns it into a proposal set.

It reads three things from the request:

- **`exploration_json`** — the focal concept, the existing concepts, the atom catalogue,
  and optionally the map theme and learner-focus data.
- **`request_source`** — one of `learner_request`, `campaign_free_reveal` or
  `atom_refresh`. An unknown value is rejected **before** any model call, so a malformed
  trigger costs nothing. A *missing* or blank value is not unknown: it defaults to
  `learner_request`.
- The **model route** — from `KG_EXPLORER_MODEL`, which is required and has no
  in-binary default.

The model may return up to **six** concept proposals and up to **six** edges. Everything
it returns is validated before it is accepted:

| Rule | Consequence of breaking it |
| --- | --- |
| an atom reference must exist in the supplied catalogue | the reference is dropped |
| an edge's two concept IDs must both exist | the edge is dropped |
| an edge must not be a self-loop | the edge is dropped |
| an edge class must be `hierarchy` or `lateral` | the edge is dropped |
| the response must be JSON | a non-JSON reply yields an **empty** proposal set |
| the raw answer must be non-empty | a blank or whitespace-only answer is an **error** (`kg_explorer: the model produced no answer`), not an empty set |

The last two rows are the ones to watch, because they fail differently. A non-JSON reply
is silence: the envelope carries `concepts`, `edges`, `model_id`, `prompt_version` and
`prompt_source`, and an empty `concepts` array with a populated `model_id` means the model
answered and nothing survived validation. A blank answer is a loud failure instead, so it
takes the retry path rather than looking like a successful empty proposal.

Also note what is **not** dropped: a proposed concept with a non-empty title is kept even
when its `atom_refs` are absent or all invalid. The bad references are filtered out; the
concept is not.

The embedded agent config (`internal/agentconfig/kg_explorer.yaml`) declares the
`explorer` sub-agent's tier (`standard`) and prompt version (`v2`). No model name is
compiled in; boot fails when `KG_EXPLORER_MODEL` is unset.

### The health endpoint does not bind — a stale framework pin, not this service

The binary documents `AGENT_HEALTH_PORT` (default `8080`), and `chora-adk-common` logs a
`health_addr` at boot:

```text
"agentdispatch.subscriber_only_boot","role":"kg_explore",...,"health_addr":"[::]:8080"
"agentdispatch.serving","role":"kg_explore",...
```

**Nothing answers on that port.** Verified in the running stack from a sibling container
and from inside this container's own network namespace, over both `127.0.0.1:8080` and
`[::1]:8080` — all refused.

The cause is the framework version pinned in `go.mod`:

```text
chora-adk-common v0.0.0-20261005110720-56af53fbecf8   ← this repo, no listener
chora-adk-common v0.0.0-20261018001513-336709b355a0   ← chora-qgen, binds correctly
```

The October 18 pseudo-version fixed the bind. `chora-companion-diagnosis` and
`chora-oe-grading` carry the same stale pin and fail the same way; `chora-qgen`'s health
port answers normally. Bumping the pin past October 18 is the fix — the service code
needs no change.

Do not gate readiness on `curl http://…:8080/healthz` for these agents until then.

## Prerequisites

| Route | What you need |
| --- | --- |
| In the stack | A working `chora-stack` deployment. NATS and the model gateway come from it. |
| Standalone | Go **1.26.6** (`go.mod` declares it), a reachable NATS JetStream broker, and a reachable model gateway. |
| Either | A logical model id the gateway understands (`KG_EXPLORER_MODEL`) and gateway identity (`CHORA_GATEWAY_TENANT_ID`, `CHORA_GATEWAY_GCID`). |

## Credentials

This service holds **one** credential, and it is internal: a caller token for the
platform's model gateway. There is no provider key here.

| Value | Where it comes from | Variable |
| --- | --- | --- |
| Gateway caller token | the stack's `GATEWAY_API_KEY` | `CHORA_GATEWAY_TOKEN` |
| Gateway tenant | the stack's `CHORA_DEFAULT_TENANT_ID` | `CHORA_GATEWAY_TENANT_ID` |
| Gateway GCID | the stack's `CHORA_DEFAULT_GCID` | `CHORA_GATEWAY_GCID` |
| NATS endpoint | the stack's NATS service; no credential locally | `NATS_URL` |
| Logical model id | the stack's `.env`; **required, no default** | `KG_EXPLORER_MODEL` |

`compose.yml` marks `GATEWAY_API_KEY` required (`${GATEWAY_API_KEY:?...}`), so the stack
will not start this agent without it. Locally the value is inert —
`GATEWAY_AUTH_ENABLED=false` — but it must be non-empty, and it must equal the `<key>`
half of `GATEWAY_API_KEYS` before gateway authentication is turned on.

`KG_EXPLORER_MODEL` is a **logical model id the gateway resolves**, not a provider model
name. The stack supplies `LongCat-2.5-Preview`; boot fails if it is empty.

The provider key behind that route belongs to the gateway, not to this agent. Where it
comes from is documented once in
[`chora-stack/README.md`](../chora-stack/README.md#credentials).

## Run it in the stack

`compose.yml` does three things:

1. Builds the image from the sibling checkout at `../chora-kg-explorer` and runs
   `/usr/local/bin/kg_explorer` as its entrypoint.
2. Starts it once `nats` is healthy, `nats-init` has completed, and `model-gateway` has
   started.
3. Publishes no host port; it `expose`s `8080`, which — per the section above — nothing
   actually listens on.

```sh
cd chora-stack
docker compose up -d kg-explorer
docker compose ps kg-explorer
```

What Compose sets:

| Variable | Stack value |
| --- | --- |
| `CHORA_GATEWAY_ENDPOINT` | `model-gateway:9090` |
| `CHORA_GATEWAY_AUDIENCE` | `chora-local` |
| `CHORA_GATEWAY_INSECURE` | `1` — plaintext gRPC inside the Compose network |
| `CHORA_ENV` | `local` |
| `AGENT_DISPATCH_ENABLED` | `true` |
| `AGENT_DISPATCH_SUBSCRIPTION` | `chora-kg-explorer.agent-dispatch-kg-explore-requested` |
| `AGENT_HEALTH_PORT` | `8080` |
| `KG_EXPLORER_MODEL` | `LongCat-2.5-Preview` |

Unlike its sibling `chora-companion-diagnosis`, the subscription Compose sets **matches**
the binary's documented default, so a publisher using either name is consumed.

The lane also has to be on at the dispatching end: the AI kernel starts `kg_exploration`
only when `KG_EXPLORATION_ENABLED` is set, which the stack does. Either half missing means
dispatches go to a subject nobody consumes — silently.

## Run it standalone

```sh
git clone https://github.com/apollo-chora/chora-kg-explorer.git
cd chora-kg-explorer
go build ./...
```

```sh
export CHORA_PROJECT_ID=local
export CHORA_GATEWAY_TENANT_ID=your-tenant-id
export CHORA_GATEWAY_GCID=your-gcid
export CHORA_GATEWAY_TOKEN=<gateway caller token>
export KG_EXPLORER_MODEL=<logical model id>
export NATS_URL=nats://localhost:4222
export AGENT_DISPATCH_ENABLED=true
go run ./cmd/kg_explorer
```

The binary takes no command-line arguments. It does not read `.env`; export the values as
above. Container build:

```sh
docker build -t chora-kg-explorer .
```

CI checks, in order:

```sh
gofmt -l .
go mod tidy
git diff --exit-code -- go.mod go.sum
go vet ./...
go test ./...
```

The suite covers configuration defaults and required variables, dispatch and gateway
identity, plugin composition, request validation, prompt construction, fail-closed
proposal parsing, and the subscriber boot path — all against in-memory and scripted
components, so it needs no broker, database or network.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `KG_EXPLORER_MODEL` | **required** (stack: `LongCat-2.5-Preview`) | gateway `logical_model_id` |
| `KG_EXPLORER_FALLBACK_MODELS` | unset | comma-separated fallback chain |
| `CHORA_PROJECT_ID` | **required** | project identity used at boot |
| `CHORA_AGENT_APP_NAME` | unset | ADK session application name |
| `CHORA_GATEWAY_ENDPOINT` | `gateway.chora.site:443` | model gateway address (stack: `model-gateway:9090`) |
| `CHORA_GATEWAY_TENANT_ID` | **required** | gateway tenant scope |
| `CHORA_GATEWAY_GCID` | **required** | gateway GCID scope |
| `CHORA_GATEWAY_TOKEN` | unset in code; **required by the stack** | gateway caller token |
| `CHORA_GATEWAY_AUDIENCE` | `https://gateway.chora.site` | ID-token audience (stack: `chora-local`) |
| `CHORA_GATEWAY_INSECURE` | unset | plaintext gRPC when truthy (stack: `1`) |
| `CHORA_ENV` | `dev` | environment label (stack: `local`) |
| `NATS_URL` | **required** | NATS JetStream endpoint |
| `AGENT_DISPATCH_ENABLED` | unset | must be `true` for the subscriber to start |
| `AGENT_DISPATCH_SUBSCRIPTION` | `chora-kg-explorer.agent-dispatch-kg-explore-requested` | consumer override |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | `5` | redelivery ceiling |
| `AGENT_HEALTH_PORT` | `8080` | the port the health listener is configured for — which does not bind |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | stdout | OTLP/gRPC trace endpoint |
| `CHORA_SERVICE_VERSION` | `dev` | OTLP `service.version` |

## Verify it works

### Do not probe the health port

As described in [What it is](#what-it-is), `AGENT_HEALTH_PORT` is logged but not bound.
Use the two checks below instead.

### The subscriber came up

```sh
docker compose logs kg-explorer --tail=20 | grep agentdispatch.serving
```

A line naming `kg_explore` and its subscription is the pass. Its absence, or a
`subscriber ... stopped` line, is the failure — most often a NATS connect error at boot.

### The broker knows the consumer

The `nats` container ships no `nats` CLI, so read the monitoring endpoint from the host:

```sh
curl -s 'http://localhost:8222/jsz?consumers=1' \
  | grep -o 'chora-kg-explorer-agent-dispatch[a-z-]*' | sort -u
```

```text
chora-kg-explorer-agent-dispatch-kg-explore-requested
```

This is the stronger check: it is the broker's own view rather than the process's claim
about itself.

### The dispatching half is on

```sh
docker compose exec -T nats wget -qO- http://ai-kernel-orchestrator:8080/readyz \
  | grep -o '"kg_exploration":{[^}]*}'
```

`"started": true` on the `kg_exploration` lane is required. With this service running and
the lane off, dispatches are published to a subject with no consumer.

## Troubleshooting

- **The health probe fails while the agent is healthy** — expected; the listener does not
  bind in this build. Use the log and broker checks above.
- **Boot fails naming `KG_EXPLORER_MODEL`** — the model id is unset. It is required with no
  default; the stack supplies `LongCat-2.5-Preview`.
- **The service exits at startup** — `AGENT_DISPATCH_ENABLED` is not `true`, or a gateway
  identity value is missing. The configuration loader rejects both.
- **The process restarts in a loop** — the pinned `chora-adk-common` returns from its serve
  call once the subscription is registered; `main.go` blocks on the signal context to keep
  the process alive. If it is looping, check the boot error rather than the framework.
- **Dispatches are published but nothing consumes them** — either the kernel's
  `KG_EXPLORATION_ENABLED` is off, or the publisher is using a subject other than
  `AGENT_DISPATCH_SUBSCRIPTION`. Compare with the broker consumer list above.
- **`concepts` comes back empty but `model_id` is populated** — the model answered and
  nothing survived validation. Common causes: atom references not in the supplied
  catalogue, edges pointing at concept IDs that are not in `existing_concepts`, or an edge
  class outside `hierarchy`/`lateral`. A non-JSON reply produces the same shape silently.
- **A malformed `request_source` costs a model call** — it should not: an unknown value is
  rejected before dispatch. If you see a model call for one, the payload shape is wrong.
- **Traces never reach Tempo** — every service in this stack logs
  `invalid target address http://otel-collector:4317 ... too many colons in address`.
  Observed in the running stack; only tracing is affected.

## API surface

No inbound HTTP API. The agent is subscriber-only: it consumes one agent-dispatch subject
and returns its result through the same bus.

| Concern | Value |
| --- | --- |
| Role | `kg_explore` |
| Consumed subject | `chora-kg-explorer.agent-dispatch-kg-explore-requested` (overridable with `AGENT_DISPATCH_SUBSCRIPTION`) |
| Request fields | `exploration_json`, `request_source` |
| Accepted `request_source` | `learner_request`, `campaign_free_reveal`, `atom_refresh` |
| Completion envelope | `concepts`, `edges`, `model_id`, `prompt_version`, `prompt_source` |
| Limits | ≤ 6 concepts, ≤ 6 edges |

The only HTTP surface is the health listener described above, which does not bind in this
build.

## Architecture

```text
cmd/kg_explorer/          service entry point
internal/agent/           prompt composition, request parsing, fail-closed output parsing
internal/agentconfig/     embedded model and prompt configuration (tier, prompt version)
internal/boot/            configuration, gateway wiring, plugins, dispatch setup, startup
Dockerfile                builds /usr/local/bin/kg_explorer
```

The interesting design choice is the fail-closed parser in `internal/agent`: anything the
model returns that does not survive the validation table is dropped, and a non-JSON reply
becomes an empty proposal set rather than an error. That keeps a bad model reply from
corrupting a learner's map, at the cost of making failures quiet — which is why the
`concepts`/`model_id` combination above is the diagnostic to look at.

## Events

Dispatch is NATS JetStream, one durable consumer for the `kg_explore` role, with the
request subject set by `AGENT_DISPATCH_SUBSCRIPTION`.

| Concern | Setting |
| --- | --- |
| Request subject | `AGENT_DISPATCH_SUBSCRIPTION`, defaulted per role |
| Redelivery ceiling | `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS`, default `5` |
| Streams | `CHORA_EVENTS` for normal dispatch, `CHORA_DLQ` for dead letters |
| Shutdown | `SIGTERM` or `SIGINT` cancels the context; a boot error exits non-zero |

The agent is a producer of completions as well as a consumer of requests: the kernel's
`publish_completed` step collects the envelope and records it against the parked dispatch.
If the completion never arrives, the park is reaped rather than waiting forever.

## Migrations

**None.** This agent has no database, ships no `migrations/` directory and is not part of
the stack's migrate one-shot set. Everything it needs arrives in the dispatch payload; the
only state it produces is the completion envelope.

If you are auditing a deployment and expect a `kg-explorer-migrate` container, there isn't
one and there should not be.
