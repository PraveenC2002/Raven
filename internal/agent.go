package raven

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"text/template"
)

//go:embed assets/templates/llm/sys_prompt.tmpl
var llmSysPromptRaw string
var llmSysPromptTmpl = template.Must(template.New("LLM system prompt").Parse(llmSysPromptRaw))

//go:embed assets/templates/llm/query.tmpl
var llmQueryRaw string
var llmQueryTmpl = template.Must(template.New("llm query template").Parse(llmQueryRaw))

type agentConf struct {
	Machine *machine
	Query   string
}

type agent struct {
	*agentConf
	toolRegistry map[llmToolName]LLMTool
	llm          LLM
	updateCh     chan string
	ravenConf    *ravenConfig
}

func newAgent(ctx context.Context, agentConf *agentConf, ravenConf *ravenConfig) (*agent, *agentErr) {

	agent := &agent{
		agentConf:    agentConf,
		toolRegistry: make(map[llmToolName]LLMTool),
		updateCh:     make(chan string, 20),
		ravenConf:    ravenConf,
	}

	err := agent.bootStrap(ctx)
	if err != nil {
		return nil, newAgentError(agentErrFatal, fmt.Errorf("agent: bootstrap: %w", err))
	}

	return agent, nil
}

func (a *agent) bootStrap(ctx context.Context) error {

	// Init tool registry
	emitToolUpdate := func(upd string) {
		select {
		case a.updateCh <- upd:
		default:
		}
	}

	type toolEntry struct {
		name llmToolName
		tool LLMTool
	}

	var tools []*toolEntry

	remoteSSH, err := newRemoteSSH(&a.Machine.connectionInfo)
	if err != nil {
		return newAgentError(err.kind, fmt.Errorf("init remote ssh: %w", err.Unwrap()))
	}

	tools = append(tools, &toolEntry{name: ToolExecuteSSH, tool: remoteSSH})

	for _, t := range tools {
		t.tool.setUpdateEmitter(emitToolUpdate)
		a.toolRegistry[t.name] = t.tool
	}

	// get system prompt
	sysPrompt, err := a.systemPrompt()
	if err != nil {
		return fmt.Errorf("build system prompt: %w", err)
	}

	// setup llm
	llm, err := newGemini(ctx, sysPrompt, a.ravenConf.geminiAPIKey)
	if err != nil {
		return fmt.Errorf("init llm: %w", err)
	}
	a.llm = llm

	return nil
}

func (a *agent) systemPrompt() (string, *agentErr) {

	var buf bytes.Buffer

	type toolManifest struct {
		Name   llmToolName
		Policy string
	}

	var toolPolicies []*toolManifest

	for name, t := range a.toolRegistry {

		policy, err := t.getToolPolicy(name)
		if err != nil {
			return "", newAgentError(agentErrFatal, err)
		}

		toolPolicies = append(toolPolicies,
			&toolManifest{
				Name:   name,
				Policy: policy,
			},
		)
	}

	err := llmSysPromptTmpl.Execute(&buf, struct {
		ToolManifest []*toolManifest
	}{
		ToolManifest: toolPolicies,
	})
	if err != nil {
		return "", newAgentError(agentErrFatal, fmt.Errorf("execute system prompt template: %w", err))
	}

	return buf.String(), nil
}

func (a *agent) run(ctx context.Context) (*llmFinalResponse, *agentErr) {

	a.emitUpdate("Starting machine " + a.Machine.Name + " diagnosis.")

	result, err := a.loop(ctx)
	if err != nil {
		return nil, newAgentError(err.kind, fmt.Errorf("agent : %w", err.Unwrap()))
	}

	err = a.cleanUp()
	if err != nil {
		return nil, newAgentError(err.kind, fmt.Errorf("agent : %w", err.Unwrap()))
	}

	a.emitUpdate("Finished machine diagnosis.")

	return result, nil
}

func (a *agent) query() (string, *agentErr) {

	var buf bytes.Buffer

	err := llmQueryTmpl.Execute(&buf, a)
	if err != nil {
		err = fmt.Errorf("execute query template: %w", err)
		return "", newAgentError(agentErrFatal, err)
	}

	return buf.String(), nil
}

func (a *agent) initialPrompt(ctx context.Context) (*llmMessage, *agentErr) {

	promptStr, err := a.query()
	if err != nil {
		return nil, newAgentError(err.kind, fmt.Errorf("construct query : %w", err.Unwrap()))
	}

	p := &llmPart{
		Text: promptStr,
	}

	parts := []*llmPart{p}

	resp, err := a.llm.generate(ctx, parts)
	if err != nil {
		return nil, newAgentError(err.kind, fmt.Errorf("generate : %w", err.Unwrap()))
	}

	return resp, nil
}

func (a *agent) emitUpdate(upd string) {
	select {
	case a.updateCh <- upd:
	default:
	}
}

func (a *agent) handleResponse(ctx context.Context, resp *llmMessage) ([]*llmFunctionResponse, *llmResponse, *agentErr) {

	funcs := a.getFunctionCalls(resp)

	if len(funcs) != 0 {
		FRs, err := a.executeFunctionCalls(ctx, funcs)
		if err != nil {
			return nil, nil, newAgentError(err.kind, fmt.Errorf("execute function calls : %w", err.Unwrap()))
		}
		return FRs, nil, nil
	}

	if len(resp.Text) != 0 {
		data := []byte(resp.Text)
		var response *llmResponse
		unmarshalErr := json.Unmarshal(data, &response)
		if unmarshalErr != nil {
			response = &llmResponse{
				clientErrors: &llmResponseErrors{
					textUnmarshalErr: fmt.Sprintf("response text unmarshal : %s", unmarshalErr.Error()),
				},
			}
		}
		return nil, response, nil
	}

	return nil, nil, newAgentError(agentErrTerminate, fmt.Errorf("empty LLM response"))
}

func (a *agent) getFunctionCalls(msg *llmMessage) []*llmFunctionCall {

	var funcs []*llmFunctionCall

	for _, p := range msg.Parts {
		if p.FunctionCall != nil {
			funcs = append(funcs, p.FunctionCall)
		}
	}

	return funcs
}

func (a *agent) executeFunctionCalls(ctx context.Context, FCs []*llmFunctionCall) ([]*llmFunctionResponse, *agentErr) {

	var results []*llmFunctionResponse

	for _, fc := range FCs {

		upd, _ := fc.Args["update"]

		if updateStr, ok := upd.(string); ok {
			a.emitUpdate(updateStr)
		}

		tool := a.toolRegistry[fc.Name]
		res, err := tool.callTool(ctx, fc)
		if err != nil {
			return nil, newAgentError(err.kind, fmt.Errorf("tool call : %w", err.Unwrap()))
		}

		results = append(results, res)

	}

	return results, nil
}

// TODO: Add max iteration bound
func (a *agent) loop(ctx context.Context) (*llmFinalResponse, *agentErr) {

	msg, err := a.initialPrompt(ctx)
	if err != nil {
		return nil, newAgentError(err.kind, fmt.Errorf("initial prompt : %w", err.Unwrap()))
	}

	FRs, res, err := a.handleResponse(ctx, msg)
	if err != nil {
		return nil, newAgentError(err.kind, fmt.Errorf("initial prompt : %w", err.Unwrap()))
	}

	for res == nil || res.FinalResponse == nil {

		var parts []*llmPart

		if res != nil && res.clientErrors != nil && len(res.clientErrors.textUnmarshalErr) != 0 {
			part := &llmPart{
				Text: res.clientErrors.textUnmarshalErr,
			}
			parts = append(parts, part)
		}

		for _, fr := range FRs {
			part := &llmPart{
				FunctionResponse: fr,
			}
			parts = append(parts, part)
		}

		if len(parts) == 0 {
			return nil, newAgentError(agentErrTerminate, fmt.Errorf("empty llm response, no parts to send"))
		}

		msg, err = a.llm.generate(ctx, parts)
		if err != nil {
			return nil, newAgentError(err.kind, fmt.Errorf("generate : %w", err.Unwrap()))
		}

		FRs, res, err = a.handleResponse(ctx, msg)
		if err != nil {
			return nil, newAgentError(err.kind, fmt.Errorf("handle response : %w", err.Unwrap()))
		}
	}

	return res.FinalResponse, nil
}

func (a *agent) getUpdates() <-chan string {
	return a.updateCh
}

func (a *agent) cleanUp() *agentErr {

	var errs []error

	for _, t := range a.toolRegistry {
		err := t.close()
		if err != nil {
			errs = append(errs, err)
		}
	}

	var cleanupErr error
	if len(errs) != 0 {
		for _, err := range errs {
			if cleanupErr == nil {
				cleanupErr = fmt.Errorf("tool registry cleanup : %w", err)
			} else {
				cleanupErr = fmt.Errorf("%w %w", cleanupErr, err)
			}
		}
		return newAgentError(agentErrFatal, fmt.Errorf("%s", cleanupErr))
	}

	// can panic if routine tries to send after closing, currently not a problem because everything is synchronous
	// if in future operations becomes asynchronous handle this
	close(a.updateCh)

	return nil
}

func newErrFr(fc *llmFunctionCall, err error) *llmFunctionResponse {
	return &llmFunctionResponse{
		ID:    fc.ID,
		Name:  fc.Name,
		Error: err.Error(),
	}
}

type agentErr struct {
	kind agentError
	err  error
}

func newAgentError(kind agentError, err error) *agentErr {
	return &agentErr{
		kind: kind,
		err:  err,
	}
}

func (e *agentErr) Error() string {
	return e.err.Error()
}

func (e *agentErr) Unwrap() error {
	return e.err
}
