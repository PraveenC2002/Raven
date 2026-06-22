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
		return nil, err.Wrap("agent: bootstrap: ")
	}

	return agent, nil
}

func (a *agent) bootStrap(ctx context.Context) *agentErr {

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
		return err.Wrap("init remote ssh: ")
	}

	tools = append(tools, &toolEntry{name: ToolExecuteSSH, tool: remoteSSH})

	for _, t := range tools {
		t.tool.setUpdateEmitter(emitToolUpdate)
		a.toolRegistry[t.name] = t.tool
	}

	// setup llm
	llm, err := newGemini(ctx, a.ravenConf.geminiAPIKey)
	if err != nil {
		return err.Wrap("init llm: ")
	}
	a.llm = llm

	return nil
}

func (a *agent) systemPrompt(currIter int) (string, *agentErr) {

	var buf bytes.Buffer

	type toolManifest struct {
		Name   llmToolName
		Policy string
	}

	var toolPolicies []*toolManifest

	for name, t := range a.toolRegistry {

		policy, err := t.getToolPolicy(name)
		if err != nil {
			return "", &agentErr{kind: agentErrFatal, err: err}
		}

		toolPolicies = append(toolPolicies,
			&toolManifest{
				Name:   name,
				Policy: policy,
			},
		)
	}

	err := llmSysPromptTmpl.Execute(&buf, struct {
		MaxIterations       int
		CurrentIteration    int
		RemainingIterations int
		ToolManifest        []*toolManifest
	}{
		MaxIterations:       agentMaxIterations,
		CurrentIteration:    currIter,
		RemainingIterations: agentMaxIterations - currIter,
		ToolManifest:        toolPolicies,
	})

	if err != nil {
		return "", &agentErr{kind: agentErrFatal, err: fmt.Errorf("execute system prompt template: %w", err)}
	}

	return buf.String(), nil
}

func (a *agent) run(ctx context.Context) (*llmFinalResponse, *agentErr) {

	a.emitUpdate("Starting " + a.Machine.Name + " machine diagnosis.")

	agentLogger.Info("Starting " + a.Machine.Name + " machine diagnosis.")

	result, err := a.loop(ctx)
	if err != nil {
		return nil, err.Wrap("agent : ")
	}

	a.emitUpdate("Finished machine diagnosis.")
	err = a.cleanUp()
	if err != nil {
		return nil, err.Wrap("agent : ")
	}

	agentLogger.Info("Finished machine diagnosis")

	return result, nil
}

func (a *agent) query() (string, *agentErr) {

	var buf bytes.Buffer

	err := llmQueryTmpl.Execute(&buf, a)
	if err != nil {
		err = fmt.Errorf("execute query template: %w", err)
		agentLogger.Error("construct client query", "line 164, error", err.Error())
		return "", &agentErr{kind: agentErrFatal, err: err}
	}

	agentLogger.Info("built client query")

	return buf.String(), nil
}

func (a *agent) initialPrompt(ctx context.Context) (*llmMessage, *agentErr) {

	promptStr, err := a.query()
	if err != nil {
		return nil, err.Wrap("construct query : ")
	}

	p := &llmPart{
		Text: promptStr,
	}

	parts := []*llmPart{p}

	sysPromptStr, err := a.systemPrompt(1)
	if err != nil {
		return nil, err
	}

	resp, err := a.llm.generate(ctx, parts, sysPromptStr)
	if err != nil {
		agentLogger.Error("Intial prompt:", "line 193, error", err.Error())
		return nil, err.Wrap("generate : ")
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
		FRs, res, err := a.executeFunctionCalls(ctx, funcs)
		if err != nil {
			agentLogger.Error("exec FCs", "error", err.Error())
			return nil, nil, err.Wrap("execute function calls : ")
		}
		if res != nil {
			return nil, res, nil
		}
		return FRs, nil, nil
	}

	// if len(resp.Text) != 0 {
	// 	data := []byte(resp.Text)
	// 	var response *llmResponse
	// 	unmarshalErr := json.Unmarshal(data, &response)
	// 	if unmarshalErr != nil {
	// 		agentLogger.Error("Handle response", "text unmarshal error", unmarshalErr.Error())
	// 		response = &llmResponse{
	// 			clientErrors: &llmResponseErrors{
	// 				textUnmarshalErr: fmt.Sprintf("response text unmarshal : %s", unmarshalErr.Error()),
	// 			},
	// 		}
	// 	}
	// 	return nil, response, nil
	// }

	return nil, nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("empty LLM response")}
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

func (a *agent) buildFinalResponse(fc *llmFunctionCall) (*llmResponse, *agentErr) {
    blob, err := json.Marshal(fc.Args)
    if err != nil {
        return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("marshal submit_report args: %w", err)}
    }

    var result diagnosisResult
    if err = json.Unmarshal(blob, &result); err != nil {
        return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("unmarshal diagnosis result: %w", err)}
    }

    return &llmResponse{
        FinalResponse: &llmFinalResponse{DiagnosisResult: &result},
    }, nil
}

func (a *agent) executeFunctionCalls(ctx context.Context, FCs []*llmFunctionCall) ([]*llmFunctionResponse, *llmResponse, *agentErr) {

	var results []*llmFunctionResponse

	for _, fc := range FCs {

		if fc.Name == ToolSubmitReport {
			res, err := a.buildFinalResponse(fc)
			if err != nil {
				return nil, nil, err.Wrap("execute function call : ")
			}
			return nil, res, nil
		}

		upd, _ := fc.Args["update"]

		if updateStr, ok := upd.(string); ok {
			a.emitUpdate(updateStr)
		}

		tool := a.toolRegistry[fc.Name]
		res, err := tool.callTool(ctx, fc)
		if err != nil {
			return nil, nil, err.Wrap("tool call : ")
		}

		results = append(results, res)

	}

	return results, nil, nil
}

func (a *agent) loop(ctx context.Context) (*llmFinalResponse, *agentErr) {

	msg, err := a.initialPrompt(ctx)
	if err != nil {
		return nil, err.Wrap("initial prompt : ")
	}

	agentLogger.Info("Made initial prompt")

	FRs, res, err := a.handleResponse(ctx, msg)
	if err != nil {
		return nil, err.Wrap("initial prompt : ")
	}

	currentIter := 2

	for res == nil || res.FinalResponse == nil {

		agentLogger.Info("loop", "iteration", currentIter)

		var parts []*llmPart

		// if res != nil && res.clientErrors != nil && len(res.clientErrors.textUnmarshalErr) != 0 {
		// 	part := &llmPart{
		// 		Text: res.clientErrors.textUnmarshalErr,
		// 	}
		// 	parts = append(parts, part)
		// }

		for _, fr := range FRs {
			part := &llmPart{
				FunctionResponse: fr,
			}
			parts = append(parts, part)
		}

		if len(parts) == 0 {
			agentLogger.Error("loop", "error", "empty llm response, no parts to send")
			return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("empty llm response, no parts to send")}
		}

		sysPromptStr, err := a.systemPrompt(currentIter)
		if err != nil {
			agentLogger.Error("loop:", "error", err.Error())
			return nil, err
		}

		msg, err = a.llm.generate(ctx, parts, sysPromptStr)
		if err != nil {
			agentLogger.Error("loop", "error", err.Error())
			return nil, err.Wrap("generate : ")
		}

		FRs, res, err = a.handleResponse(ctx, msg)
		if err != nil {
			return nil, err.Wrap("handle response : ")
		}
		currentIter++
		if currentIter > agentMaxIterations {
			break
		}
	}

	if currentIter > agentMaxIterations {
		agentLogger.Warn("loop", "warning", "exceeded max iterations")
		aErr := &agentErr{
			kind: agentErrTerminate,
			err: fmt.Errorf(
				"%w: limit=%d",
				agentErrMaxIterationsExceeded,
				agentMaxIterations,
			),
		}
		if res == nil {
			agentLogger.Warn("loop", "warning", "nil output")
			return nil, aErr
		}
		return res.FinalResponse, aErr
	}

	agentLogger.Info("agent finished looping, returning final response")
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
		agentLogger.Error("cleanup", "error", cleanupErr.Error())
		return &agentErr{kind: agentErrFatal, err: fmt.Errorf("%s", cleanupErr)}
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

func (e *agentErr) Error() string {
	return e.err.Error()
}

func (e *agentErr) Unwrap() error {
	return e.err
}

func (e *agentErr) Wrap(msg string) *agentErr {
	return &agentErr{
		kind: e.kind,
		err:  fmt.Errorf("%s %w", msg, e.err),
	}
}
