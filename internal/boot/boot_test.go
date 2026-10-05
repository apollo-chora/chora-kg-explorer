package boot

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/tracing"
)

func setGatewayEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CHORA_PROJECT_ID", "p1")
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "t1")
	t.Setenv("CHORA_GATEWAY_GCID", "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	t.Setenv("KG_EXPLORER_MODEL", "")
	t.Setenv("CHORA_AGENT_APP_NAME", "")
}

func TestLoadConfig_guardsAndDefaults(t *testing.T) {
	t.Setenv("CHORA_PROJECT_ID", "")
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "")
	t.Setenv("CHORA_GATEWAY_GCID", "")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CHORA_PROJECT_ID") {
		t.Fatalf("project guard: %v", err)
	}
	t.Setenv("CHORA_PROJECT_ID", "p1")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CHORA_GATEWAY_TENANT_ID") {
		t.Fatalf("gateway scope guard: %v", err)
	}
	setGatewayEnv(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "gemini-2.5-flash" || cfg.PromptVersion != "v2" || len(cfg.FallbackModels) != 1 || cfg.GatewayEndpoint != "gateway.chora.site:443" || cfg.Env != "dev" {
		t.Fatalf("config = %+v", cfg)
	}
	t.Setenv("KG_EXPLORER_MODEL", "gemini-2.5-pro")
	cfg, _ = LoadConfig()
	if cfg.Model != "gemini-2.5-pro" || cfg.FallbackModels[0] != "gemini-2.5-flash-lite" {
		t.Fatalf("override must replace the primary only: %+v", cfg)
	}
	attrs := cfg.LogAttrs()
	joined := strings.Join(strings.Fields(strings.TrimSpace(strings.ReplaceAll(strings.Trim(strings.Join(toStrings(attrs), " "), "[]"), "\n", " "))), " ")
	if !strings.Contains(joined, "surface kg_exploration") || strings.Contains(joined, "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b") {
		t.Fatalf("log attrs must name the surface and truncate the gcid: %s", joined)
	}
}

func toStrings(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		b, _ := json.Marshal(v)
		out = append(out, strings.Trim(string(b), `"`))
	}
	return out
}

func TestDispatchIdentityAndGatewayConfig(t *testing.T) {
	cfg := Config{AgentAppName: "app-x", GatewayEndpoint: "gw:443", GatewayTenantID: "t1", GatewayGCID: "g1", Model: "m", FallbackModels: []string{"f"}}
	sc := dispatchServeConfig(cfg, nil, nil)
	if sc.AgentRole != "kg_explore" || sc.ServiceName != "chora-kg-explorer" || sc.AppName != "app-x" || sc.Sessions == nil || sc.SessionKey != nil || sc.UserMessage != nil {
		t.Fatalf("serve config = %+v", sc)
	}
	gc := gatewayConfig(cfg)
	if gc.AgentID != "kg_explorer" || gc.Surface != "kg_exploration" || gc.CrewKind != "kg_exploration" || gc.LogicalModelID != "m" || len(gc.FallbackModelIDs) != 1 || gc.TenantID != "t1" || gc.GCID != "g1" || gc.Endpoint != "gw:443" {
		t.Fatalf("gateway config = %+v", gc)
	}
	tc := TerminationConfig()
	if tc.AgentID != "kg_explorer" || tc.CrewKind != "kg_exploration" || tc.MaxIterations != 2 || tc.CrewPattern != "P1_SINGLE_AGENT" {
		t.Fatalf("termination = %+v", tc)
	}
}

func TestActionCodeResolver(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if ActionCode(get(map[string]string{})) != "knowledge_graph_traverse" {
		t.Fatal("absent request_source is a learner request, metered")
	}
	if ActionCode(get(map[string]string{"request_source": "campaign_free_reveal"})) != "" || ActionCode(get(map[string]string{"request_source": "atom_refresh"})) != "" {
		t.Fatal("free reveal and atom refresh are un-metered")
	}
	if ActionCode(get(map[string]string{"request_source": "instructor_push"})) != "" {
		t.Fatal("an unknown source resolves empty (the agent refuses it by name)")
	}
}

func TestRefuseArgs(t *testing.T) {
	if err := refuseArgs(nil); err != nil {
		t.Fatal(err)
	}
	if err := refuseArgs([]string{"web", "-port", "8080"}); err == nil || !strings.Contains(err.Error(), "subscriber-only") {
		t.Fatalf("args must be refused by name: %v", err)
	}
}

func TestNewPlugins_chain(t *testing.T) {
	ps, err := NewPlugins()
	if err != nil || len(ps) != 3 {
		t.Fatalf("chain: %d %v", len(ps), err)
	}
	if !strings.Contains(ps[0].Name(), "inbound_trace") || !strings.Contains(ps[1].Name(), "tenant_propagation") || !strings.Contains(ps[2].Name(), "termination") {
		t.Fatalf("chain order: %q %q %q", ps[0].Name(), ps[1].Name(), ps[2].Name())
	}
}

// ---- runner harness ----

type scriptedLLM struct {
	answers []string
	calls   []*adkmodel.LLMRequest
}

func (s *scriptedLLM) Name() string { return "scripted" }
func (s *scriptedLLM) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		s.calls = append(s.calls, req)
		if len(s.answers) == 0 {
			yield(nil, errors.New("scriptedLLM: no answer scripted"))
			return
		}
		text := s.answers[0]
		s.answers = s.answers[1:]
		yield(&adkmodel.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}}, nil)
	}
}

func systemInstruction(req *adkmodel.LLMRequest) string {
	if req == nil || req.Config == nil || req.Config.SystemInstruction == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range req.Config.SystemInstruction.Parts {
		if p != nil {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func testConfig() Config {
	return Config{Model: "gemini-2.5-flash", FallbackModels: []string{"gemini-2.5-flash-lite"}, PromptVersion: "v2"}
}

// runRoot runs the root agent through the real runner, optionally under the
// production plugin chain, over a fresh session seeded with state.
func runRoot(t *testing.T, root agent.Agent, state map[string]any, plugins []*plugin.Plugin) (string, error) {
	t.Helper()
	ctx := context.Background()
	svc := session.InMemoryService()
	const app, user, sid = "kg_explorer", "11111111-1111-7111-8111-111111111111:g1", "dispatch:e-1"
	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: app, UserID: user, SessionID: sid, State: state}); err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: app, Agent: root, SessionService: svc, PluginConfig: runner.PluginConfig{Plugins: plugins}})
	if err != nil {
		t.Fatal(err)
	}
	var events []*session.Event
	for ev, err := range r.Run(ctx, user, sid, &genai.Content{Role: "user", Parts: []*genai.Part{{Text: "BEGIN"}}}, agent.RunConfig{}) {
		if err != nil {
			return "", err
		}
		events = append(events, ev)
	}
	return agentdispatch.TerminalText(events, agentdispatch.TerminalAuthor(DispatchRole))
}

const sampleExploration = `{"focal_title":"Fractions","focal_atom_refs":["atom-1"],"existing_concepts":[{"concept_id":"c-1","title":"Fractions"},{"concept_id":"c-2","title":"Decimals"}],"map_theme":"Primary maths","atom_catalogue":[{"atom_id":"atom-1","title":"Halves","atom_type":"mcq","topic_tags":["fractions"],"relevance":"on_theme"},{"atom_id":"atom-2","title":"Tenths","atom_type":"mcq","topic_tags":["decimals"],"relevance":"related"}],"request_source":"learner_request","sub_goal":"See equivalent fractions","goal_title":"Fractions mastery","ancestors":["Number"],"weakness":{"descriptor":"confuses numerator and denominator"}}`

func baseState(extra map[string]any) map[string]any {
	st := map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1", "exploration_json": sampleExploration}
	for k, v := range extra {
		st[k] = v
	}
	return st
}

func TestNewAgent_happyPathRendersTheFailClosedEnvelope(t *testing.T) {
	llm := &scriptedLLM{answers: []string{"```json\n" + `{"concepts":[{"title":"Equivalent fractions","rationale":"same value","atom_refs":["atom-1","atom-FAKE"]},{"title":"Ratios","rationale":"next door","atom_refs":[]}],"edges":[{"source_concept_id":"c-1","target_concept_id":"c-2","edge_class":"lateral","rationale":"both name parts"},{"source_concept_id":"c-1","target_concept_id":"c-9","edge_class":"hierarchy","rationale":"invented id"}]}` + "\n```"}}
	root, err := NewAgent(testConfig(), llm)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name() != "kg_explorer" || len(root.SubAgents()) != 2 {
		t.Fatalf("tree = %v", root)
	}
	plugins, err := NewPlugins()
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, root, baseState(nil), plugins)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Concepts []struct {
			Title    string   `json:"title"`
			AtomRefs []string `json:"atom_refs"`
		} `json:"concepts"`
		Edges []struct {
			Source string `json:"source_concept_id"`
			Target string `json:"target_concept_id"`
			Class  string `json:"edge_class"`
		} `json:"edges"`
		ModelID       string `json:"model_id"`
		PromptVersion string `json:"prompt_version"`
		PromptSource  string `json:"prompt_source"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope not JSON: %v: %s", err, out)
	}
	if len(env.Concepts) != 2 || env.Concepts[0].Title != "Equivalent fractions" || len(env.Concepts[0].AtomRefs) != 1 || env.Concepts[0].AtomRefs[0] != "atom-1" {
		t.Fatalf("concepts not fail-closed: %s", out)
	}
	if len(env.Edges) != 1 || env.Edges[0].Target != "c-2" || env.Edges[0].Class != "lateral" {
		t.Fatalf("edges not fail-closed: %s", out)
	}
	if env.ModelID != "gemini-2.5-flash" || env.PromptVersion != "v2" || env.PromptSource != "embedded_fallback" {
		t.Fatalf("attribution: %s", out)
	}
	if len(llm.calls) != 1 {
		t.Fatalf("one model call expected, got %d", len(llm.calls))
	}
	inst := systemInstruction(llm.calls[0])
	for _, want := range []string{"Focal concept: Fractions", "MAP THEME: Primary maths", "atom-1 | Halves | mcq | fractions | on_theme", "LEARNER FOCUS FOR THIS NODE (data):", "confuses numerator and denominator"} {
		if !strings.Contains(inst, want) {
			t.Fatalf("instruction lacks %q:\n%s", want, inst)
		}
	}
	// The metered action code rides the model request for a learner request.
	if llm.calls[0].Config == nil || llm.calls[0].Config.Labels["chora_action_code"] != "knowledge_graph_traverse" {
		t.Fatalf("action code label missing: %+v", llm.calls[0].Config)
	}
}

func TestNewAgent_modelGarbageYieldsZeroProposals(t *testing.T) {
	llm := &scriptedLLM{answers: []string{"I would rather not."}}
	root, _ := NewAgent(testConfig(), llm)
	out, err := runRoot(t, root, baseState(map[string]any{"request_source": "campaign_free_reveal"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"concepts":[]`) || !strings.Contains(out, `"edges":[]`) {
		t.Fatalf("garbage must render empty lists, not null or an error: %s", out)
	}
}

func TestNewAgent_badPayloadIsPermanentBeforeTheModel(t *testing.T) {
	var perm *agentdispatch.PermanentError
	for name, st := range map[string]map[string]any{
		"missing exploration_json": {"tenant_id": "t", "user_gcid": "g"},
		"non-object exploration":   {"tenant_id": "t", "user_gcid": "g", "exploration_json": "[1,2]"},
		"unknown request_source":   baseState(map[string]any{"request_source": "instructor_push"}),
	} {
		llm := &scriptedLLM{answers: []string{"never"}}
		root, _ := NewAgent(testConfig(), llm)
		_, err := runRoot(t, root, st, nil)
		if !errors.As(err, &perm) || len(llm.calls) != 0 {
			t.Fatalf("%s: err=%v calls=%d", name, err, len(llm.calls))
		}
		if name == "unknown request_source" && !strings.Contains(err.Error(), "unknown_request_source") {
			t.Fatalf("%s: reason token missing: %v", name, err)
		}
	}
	// The payload's own request_source is honoured when the top-level key is absent.
	llm := &scriptedLLM{answers: []string{`{"concepts":[],"edges":[]}`}}
	root, _ := NewAgent(testConfig(), llm)
	st := baseState(nil)
	st["exploration_json"] = strings.Replace(sampleExploration, `"request_source":"learner_request"`, `"request_source":"atom_refresh"`, 1)
	if _, err := runRoot(t, root, st, nil); err != nil {
		t.Fatal(err)
	}
	cond := conditions(stateOf(st))
	if cond["request_source"] != "atom_refresh" || cond["themed"] != "true" || cond["focused"] != "true" {
		t.Fatalf("conditions = %v", cond)
	}
}

type mapState map[string]any

func (m mapState) Get(k string) (any, error) {
	if v, ok := m[k]; ok {
		return v, nil
	}
	return nil, session.ErrStateKeyNotExist
}

func stateOf(m map[string]any) stateReader { return mapState(m) }

func TestRun_composesTheLaneAndSurfacesTheSubscriberError(t *testing.T) {
	setGatewayEnv(t)
	origTracing, origLLM, origServe := initTracing, newGatewayLLM, serveSubscriber
	t.Cleanup(func() { initTracing, newGatewayLLM, serveSubscriber = origTracing, origLLM, origServe })
	initTracing = func(context.Context, string) (func(context.Context) error, error) {
		return func(context.Context) error { return nil }, nil
	}
	newGatewayLLM = func(context.Context, Config) (adkmodel.LLM, error) { return &scriptedLLM{}, nil }
	var got agentdispatch.ServeConfig
	errStop := errors.New("stop")
	serveSubscriber = func(_ context.Context, cfg agentdispatch.ServeConfig, _ agentdispatch.RunOptions) error {
		got = cfg
		return errStop
	}
	if err := Run(context.Background(), []string{"web"}); err == nil || !strings.Contains(err.Error(), "subscriber-only") {
		t.Fatalf("args must be refused before config: %v", err)
	}
	if err := Run(context.Background(), nil); !errors.Is(err, errStop) {
		t.Fatalf("want the subscriber error to surface, got %v", err)
	}
	if got.AgentRole != "kg_explore" || got.ServiceName != "chora-kg-explorer" || got.RootAgent == nil || len(got.Plugins.Plugins) != 3 {
		t.Fatalf("serve config = role %q service %q plugins=%d", got.AgentRole, got.ServiceName, len(got.Plugins.Plugins))
	}
	// A gateway client that cannot start is fatal before serving.
	newGatewayLLM = func(context.Context, Config) (adkmodel.LLM, error) { return nil, errors.New("no gateway") }
	if err := Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "no gateway") {
		t.Fatalf("gateway failure must be fatal: %v", err)
	}
	_ = tracing.Init
}
