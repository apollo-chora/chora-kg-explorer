package boot

import (
	"errors"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/promptstamping"

	kagent "github.com/apollo-chora/chora-kg-explorer/internal/agent"
)

// Agent tree names. The model sub-agent writes its raw answer under
// stateKeyModelRaw; the finaliser renders the fail-closed envelope as the LAST
// text event, which is what agentdispatch picks up (TerminalAuthor is empty
// for this role: last text event wins).
const (
	modelAgentName     = CrewKind + "_model"
	finaliserAgentName = CrewKind + "_finalise"
	stateKeyModelRaw   = "kg_explorer_model_raw"
)

type stateReader interface {
	Get(string) (any, error)
}

// readRequest decodes and validates the dispatch payload: exploration_json is
// the request, request_source selects the action code. Both faults are
// permanent (no redelivery can supply a payload).
func readRequest(st stateReader) (kagent.Request, error) {
	r, err := kagent.ParseRequest(StateString(st, stateKeyExplorationJSON))
	if err != nil {
		return kagent.Request{}, agentdispatch.Permanent("invalid_exploration_json", err)
	}
	source := StateString(st, stateKeyRequestSource)
	if strings.TrimSpace(source) == "" {
		source = r.RequestSource
	}
	if _, err := kagent.ActionCodeFor(source); err != nil {
		return kagent.Request{}, agentdispatch.Permanent(err.Error(), nil)
	}
	return r, nil
}

// instructionProvider composes the concept-suggestion prompt from the payload.
func instructionProvider() llmagent.InstructionProvider {
	return func(ctx agent.ReadonlyContext) (string, error) {
		r, err := readRequest(ctx.ReadonlyState())
		if err != nil {
			return "", err
		}
		return kagent.BuildPrompt(r), nil
	}
}

// conditions surfaces the prompt discriminants (ADR-197 M-A): whether the
// catalogue is themed / banded and whether a learner focus block rode in.
func conditions(st stateReader) map[string]string {
	cond := map[string]string{}
	r, err := kagent.ParseRequest(StateString(st, stateKeyExplorationJSON))
	if err != nil {
		return cond
	}
	cond["themed"] = fmt.Sprint(strings.TrimSpace(r.MapTheme) != "")
	focused := strings.TrimSpace(r.SubGoal) != "" || strings.TrimSpace(r.GoalTitle) != "" || len(r.Ancestors) > 0 || r.Weakness != nil
	cond["focused"] = fmt.Sprint(focused)
	source := StateString(st, stateKeyRequestSource)
	if strings.TrimSpace(source) == "" {
		source = r.RequestSource
	}
	if strings.TrimSpace(source) == "" {
		source = kagent.SourceLearnerRequest
	}
	cond["request_source"] = strings.ToLower(strings.TrimSpace(source))
	return cond
}

// Prompt-source values of the envelope (A5 visibility, ADR-254 D9): the
// ADR-197 registry resolved a version, or the embedded default served.
const (
	promptSourceRegistry = "registry"
	promptSourceEmbedded = "embedded_fallback"
)

// promptSourceOf reports which prompt served the turn: registry when the
// stamping layer resolved a registry version into state, else the embedded
// default. Same rule as companion_chat's envelope.
func promptSourceOf(st stateReader) string {
	if strings.TrimSpace(StateString(st, promptstamping.StateKeyResolvedPromptVersion)) != "" {
		return promptSourceRegistry
	}
	return promptSourceEmbedded
}

// finalise parses the model's raw answer fail-closed against the request's
// id sets and renders the completion envelope.
func finalise(st stateReader, modelID, promptVersion, promptSource string) (string, error) {
	r, err := readRequest(st)
	if err != nil {
		return "", err
	}
	raw := StateString(st, stateKeyModelRaw)
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("kg_explorer: the model produced no answer")
	}
	existing, catalogue := kagent.IDSets(r)
	concepts, edges := kagent.ParseOutput(raw, existing, catalogue)
	out := kagent.Output{Concepts: concepts, Edges: edges, ModelID: modelID, PromptVersion: promptVersion, PromptSource: promptSource}
	return out.Render(), nil
}

// NewAgent builds the kg_explorer root: the model sub-agent (OutputKey) then
// the deterministic finaliser, run in sequence under one root.
func NewAgent(cfg Config, llm adkmodel.LLM) (agent.Agent, error) {
	if llm == nil {
		return nil, errors.New("NewAgent: LLM is required")
	}
	stamped := promptstamping.WithStamping(cfg.PromptVersion,
		func(s session.ReadonlyState) map[string]string { return conditions(s) },
		instructionProvider())
	modelA, err := llmagent.New(llmagent.Config{
		Name:                modelAgentName,
		Model:               llm,
		OutputKey:           stateKeyModelRaw,
		InstructionProvider: stamped,
	})
	if err != nil {
		return nil, fmt.Errorf("llmagent.New(%s): %w", modelAgentName, err)
	}
	finaliser, err := agent.New(agent.Config{
		Name:        finaliserAgentName,
		Description: "fail-closed parse of the proposals and the envelope render",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				text, err := finalise(ctx.Session().State(), cfg.Model, cfg.PromptVersion, promptSourceOf(ctx.Session().State()))
				if err != nil {
					yield(nil, err)
					return
				}
				ev := session.NewEvent(ctx.InvocationID())
				ev.Author = finaliserAgentName
				ev.Content = &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}
				yield(ev, nil)
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("agent.New(%s): %w", finaliserAgentName, err)
	}
	return agent.New(agent.Config{
		Name:        CrewKind,
		Description: "kg_explore: concept + edge proposals for the learner's Discovery map",
		SubAgents:   []agent.Agent{modelA, finaliser},
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				// Validate before the model is paid for: a bad payload is a
				// permanent failure raised here, reported on the wire as FAILED.
				if _, err := readRequest(ctx.Session().State()); err != nil {
					yield(nil, err)
					return
				}
				for _, child := range []agent.Agent{modelA, finaliser} {
					for ev, err := range child.Run(ctx) {
						if !yield(ev, err) {
							return
						}
						if err != nil {
							return
						}
					}
				}
			}
		},
	})
}
