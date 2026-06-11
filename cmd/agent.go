package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"
)

type agent struct {
	Machine           *machine
	llm               LLM
	emitInterimUpdate func()
	Query             string
	toolRegistry      map[string]LLMTool
	errPrefix         string
}

func newAgent(query string) *agent {

	agent := &agent{
		toolRegistry: make(map[string]LLMTool),
		errPrefix:    "agent error :",
	}

	agent.bootStrap()

	return agent
}

func (a *agent) bootStrap() error {
	remoteSSH, err := newRemoteSSH(&connectionInfo{})
	if err != nil {
		return err
	}
	a.toolRegistry[ToolExecuteSSH] = remoteSSH
	return nil
}

func (a *agent) userQuery() (string, *agentErr) {

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
		err = fmt.Errorf("%s query template parsing error.\nError : %s", a.errPrefix, err.Error())
		return "", newAgentError(agentErrFatal, err)
	}

	var buf bytes.Buffer

	err = tmpl.Execute(&buf, a)
	if err != nil {
		err = fmt.Errorf("%s query template execution error.\nError : %s", a.errPrefix, err.Error())
		return "", newAgentError(agentErrFatal, err)
	}

	return buf.String(), nil
}

func (a *agent) initialPrompt() (*llmMessage, *agentErr) {

	promptStr, err := a.userQuery()
	if err != nil {
		return nil, err
	}

	p := &llmPart{
		Text: promptStr,
	}

	parts := []*llmPart{p}

	resp, err := a.llm.generate(context.Background(), parts)
	if err != nil {
		return nil, err
	}

	return resp, nil
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

func (a *agent) handleResponse(resp *llmMessage) ([]*llmFunctionResponse, *llmResponse, *agentErr) {

	funcs := a.getFunctionCalls(resp)

	if len(funcs) != 0 {
		FRs, err := a.executeFunctionCalls(funcs)
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
					textUnmarshalErr: fmt.Sprintf("%s response unmarshal error.\nError : %s", a.errPrefix, unmarshalErr.Error()),
				},
			}
		}
		return nil, response, nil
	}

	return nil, nil, newAgentError(agentErrTerminate, fmt.Errorf("%s received empty response from LLM", a.errPrefix))
}

func (a *agent) executeFunctionCalls(funcs []*llmFunctionCall) ([]*llmFunctionResponse, *agentErr) {

	var results []*llmFunctionResponse

	for _, fn := range funcs {

		tool := a.toolRegistry[fn.Name]
		res, err := tool.toolCall(fn)
		if err != nil {
			return nil, err
		}

		results = append(results, res)
	}

	return results, nil
}

func (a *agent) loop() *agentErr {

	msg, err := a.initialPrompt()
	if err != nil {
		return err
	}

	FRs, res, err := a.handleResponse(msg)
	if err != nil {
		return err
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
			return newAgentError(agentErrTerminate, fmt.Errorf("%s zero parts to send. Empty LLM response", a.errPrefix))
		}

		msg, err = a.llm.generate(context.Background(), parts)
		if err != nil {
			return err
		}

		FRs, res, err = a.handleResponse(msg)
		if err != nil {
			return err
		}
	}

	return nil
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
