package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"
)

type agentConf struct {
	Machine *machine
	Query   string
}

type agent struct {
	*agentConf
	toolRegistry map[string]LLMTool
	llm          LLM
	updateCh     chan string
	errDomain    string
}

func newAgent(ctx context.Context, conf *agentConf) (*agent, error) {

	agent := &agent{
		agentConf:    conf,
		toolRegistry: make(map[string]LLMTool),
		updateCh:     make(chan string, 20),
		errDomain:    "agent error :",
	}

	err := agent.bootStrap(ctx)
	if err != nil {
		return nil, err
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
		name string
		tool LLMTool
	}

	var tools []*toolEntry

	remoteSSH, err := newRemoteSSH(a.Machine.connectionInfo)
	if err != nil {
		return err
	}
	tools = append(tools, &toolEntry{name: ToolExecuteSSH, tool: remoteSSH})

	for _, t := range tools {
		t.tool.setUpdateEmitter(emitToolUpdate)
		a.toolRegistry[t.name] = t.tool
	}

	// get system prompt
	sysPrompt, err := a.systemPrompt()
	if err != nil {
		return err
	}

	// setup llm
	llm, err := newGemini(ctx, sysPrompt)
	if err != nil {
		return err
	}
	a.llm = llm

	return nil
}

func (a *agent) systemPrompt() (string, error) {

	const systemPromptTemplate = `
	You are Raven, an autonomous SRE diagnosis agent.
	You have been deployed to diagnose a service running on a remote machine that is experiencing issues.
	Your sole purpose is to diagnose the root cause.
	You do not fix, modify, restart, or write anything — you only investigate and report.

	---

	Tool usage policy for all tools available:

	{{range .ToolManifest}}

	Tool Name : {{.Name}}
	Tool Policy :
	{{.Policy}}

	{{end}}
	---

	## Diagnosis Methodology
	Work systematically, layer by layer:

	1. **Orient** — identify OS, distro, uptime, recent reboots
	2. **Locate** — find the affected service, check its status
	3. **Examine logs** — look for errors, panics, OOMs, segfaults
	4. **Check resources** — disk space, memory, CPU pressure
	5. **Check dependencies** — databases, upstream services, network connectivity
	6. **Correlate** — connect findings to form a root cause hypothesis
	7. **Verify** — confirm your hypothesis with one or two targeted commands before reporting

	---

	## Common Services
	These machines typically run one or more of the following:

	- **Next.js apps** — via PM2 or systemd, check process status and PM2 logs
	- **Dockerized services** — check container status, resource usage, container logs
	- **PostgreSQL / MySQL / Redis** — check service status, connection counts, error logs
	- **Nginx** — reverse proxy and rate limiting, check error logs, config validity, upstream connectivity
	- **Systemd services** — check unit status, journal logs
	- **Node.js / Python backends** — check process status, application logs

	---

	## Reasoning
	Before every tool call, explicitly state your reasoning. — what you know so far, what you suspect, and why this specific command advances the investigation. Do not run commands out of habit or checklist mentality. Every command must have a clear purpose given what you already know.

	---

	## Termination
	Stop issuing commands when you have sufficient evidence to confidently explain the root cause. Do not over-investigate. If the root cause remains genuinely unclear after thorough investigation, report your best hypothesis with low confidence and the supporting evidence you found.

	---

	## Constraints
	- Strictly read-only. Never attempt to modify, write, delete, move, or restart anything.
	- Do not make assumptions — verify with commands.
	- Do not retry a rejected command. Reason about an alternative approach.
	`

	tmpl, err := template.New("LLM system prompt").Parse(systemPromptTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer

	type toolManifest struct {
		Name   string
		Policy string
	}

	var toolPolicies []*toolManifest

	for name, t := range a.toolRegistry {

		policy, err := t.getToolPolicy(name)
		if err != nil {
			return "", err
		}

		toolPolicies = append(toolPolicies,
			&toolManifest{
				Name:   name,
				Policy: policy,
			},
		)
	}

	err = tmpl.Execute(&buf, struct {
		ToolManifest []*toolManifest
	}{
		ToolManifest: toolPolicies,
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (a *agent) run(ctx context.Context) (any, *agentErr) {

	// can panic if routine tries to send after closing, currently not a problem because everything is synchronous
	// if in future operations becomes asynchronous handle this
	defer close(a.updateCh)

	a.emitUpdate("Starting machine " + a.Machine.Name + " diagnosis.")

	result, err := a.loop(ctx)
	if err != nil {
		return nil, err
	}

	err = a.cleanUp()
	if err != nil {
		return nil, err
	}
	
	a.emitUpdate("Finished machine diagnosis.")

	return result, nil
}

func (a *agent) query() (string, *agentErr) {

	queryTemplate := `
	Machine Information :
		Machine name : {{.Machine.Name}}
		Machine Description :
			{{.Machine.Description}}
	User Query :
		{{.Query}}
	`

	tmpl, err := template.New("query template").Parse(queryTemplate)
	if err != nil {
		err = fmt.Errorf("%s query template parsing error.\nError : %s", a.errDomain, err.Error())
		return "", newAgentError(agentErrFatal, err)
	}

	var buf bytes.Buffer

	err = tmpl.Execute(&buf, a)
	if err != nil {
		err = fmt.Errorf("%s query template execution error.\nError : %s", a.errDomain, err.Error())
		return "", newAgentError(agentErrFatal, err)
	}

	return buf.String(), nil
}

func (a *agent) initialPrompt(ctx context.Context) (*llmMessage, *agentErr) {

	promptStr, err := a.query()
	if err != nil {
		return nil, err
	}

	p := &llmPart{
		Text: promptStr,
	}

	parts := []*llmPart{p}

	resp, err := a.llm.generate(ctx, parts)
	if err != nil {
		return nil, err
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
			return nil, nil, err
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
					textUnmarshalErr: fmt.Sprintf("%s response unmarshal error.\nError : %s", a.errDomain, unmarshalErr.Error()),
				},
			}
		}
		return nil, response, nil
	}

	return nil, nil, newAgentError(agentErrTerminate, fmt.Errorf("%s received empty response from LLM", a.errDomain))
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
			return nil, err
		}

		results = append(results, res)

	}

	return results, nil
}

// TODO: Add max iteration bound
func (a *agent) loop(ctx context.Context) (any, *agentErr) {

	msg, err := a.initialPrompt(ctx)
	if err != nil {
		return nil, err
	}

	FRs, res, err := a.handleResponse(ctx, msg)
	if err != nil {
		return nil, err
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
			return nil, newAgentError(agentErrTerminate, fmt.Errorf("%s zero parts to send. Empty LLM response", a.errDomain))
		}

		msg, err = a.llm.generate(ctx, parts)
		if err != nil {
			return nil, err
		}

		FRs, res, err = a.handleResponse(ctx, msg)
		if err != nil {
			return nil, err
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

	if len(errs) != 0 {
		errStr := fmt.Sprintf("%s error while agent cleanup\n", a.errDomain)
		for _, err := range errs {
			errStr = errStr + fmt.Sprintf("Error : %s\n", err.Error())
		}
		return newAgentError(agentErrFatal, fmt.Errorf("%s", errStr))
	}

	return nil
}

func newErrFr(fc *llmFunctionCall, err error) *llmFunctionResponse {
	return &llmFunctionResponse{
		ID:     fc.ID,
		Name:   fc.Name,
		Result: err.Error(),
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
