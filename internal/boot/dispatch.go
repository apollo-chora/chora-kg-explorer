package boot

// Dispatch identity for the ADR-253/254 NATS JetStream lane. Three strings
// are in play and they are deliberately different, exactly as on every other
// lane:
//
//	CrewKind      kg_explorer          the ADK agent name; the event AUTHOR
//	DispatchRole  kg_explore           what the kennel keys subjects on
//	ServiceName   chora-kg-explorer    the service name; names the subscription
//
// The request subscription (chora-kg-explorer.agent-dispatch-kg-explore-requested)
// is set explicitly via AGENT_DISPATCH_SUBSCRIPTION; the derived name is the
// fallback.
const (
	DispatchRole = "kg_explore"
	ServiceName  = "chora-kg-explorer"
)

// Payload keys the dispatch carries for this role (ADR-254 D6 addendum): the
// exploration request as one JSON object under exploration_json, and the
// request_source discriminator that selects the gateway action code (D7).
const (
	stateKeyExplorationJSON = "exploration_json"
	stateKeyRequestSource   = "request_source"
)
