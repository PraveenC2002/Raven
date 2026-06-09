package main

import (
	"bytes"
	"context"
	"text/template"
	"time"

	"google.golang.org/genai"
)

type agentStep struct {
	StepNumber  int
	Reasoning   string
	ToolCall    string
	ToolOutput  string
	Observation string
	FinishedAt  time.Time
}

type agentState struct {
	Machine   machine
	UserQuery string
	Steps     []*agentStep
}

func (as *agentState) renderState() (string, error) {

	stateTemplate := `
	Context :

	Machine Name : {{.Machine.Name}}
	Machine Description : {{.Machine.Description}}
	User query : {{.UserQuery}}

	Your investigation history :

	{{range .Steps}}
	Step number : {{.StepNumber}}
	Tool call : {{.ToolCall}}
	Reasoning : {{.Reasoning}}
	Tool output : {{.ToolOutput}}
	Observation : {{.Observation}}
	Finished at : {{.FinishedAt}}

	{{end}}
	`

	t, err := template.New("Agent State Template").Parse(stateTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, as)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

type agent struct {
	llm               LLM
	emitInterimUpdate func()
	state             *agentState
	toolRegistry      map[string]LLMTool
}

func newAgent(query string) *agent {

	agent := &agent{
		toolRegistry: make(map[string]LLMTool),
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

func (a *agent) prompt() error {
	resp, err := a.llm.generate(context.Background(), "")
	if err != nil {
		return err
	}

	funcs := a.llm.getFunctionCalls(resp)

	
	if len(funcs) != 0 {
		a.executeFunctionCalls(funcs)
	}

	
	return nil
}

func (a *agent) executeFunctionCalls(funcs []*genai.FunctionCall) []any {

	var results []any

	for _, fn := range funcs {
		tool := a.toolRegistry[fn.Name]
		res, _ := tool.toolCall(fn)
		results = append(results, res)
	}

	return results
}

