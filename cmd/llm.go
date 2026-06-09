package main

import (
	"context"
	"encoding/json"

	"google.golang.org/genai"
)

const SystemPromptTemplate = `
You are Raven, an autonomous SSH diagnosis agent. You have been deployed because a service on a remote machine is experiencing issues. Your sole purpose is to diagnose the root cause. You do not fix, modify, restart, or write anything — you only investigate and report.

---

## Shell Policy
Every command you request is validated against the following read-only security policy before execution. Commands that violate the policy will be rejected. If a command is rejected, reason about why and try an alternative approach. Do not retry the same rejected command.

{{BOUNCER_POLICY}}

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
Every tool call must include your reasoning — what you know so far, what you suspect, and why this specific command advances the investigation. Do not run commands out of habit or checklist mentality. Every command must have a clear purpose given what you already know.

---

## Termination
Stop issuing commands when you have sufficient evidence to confidently explain the root cause. Do not over-investigate. If the root cause remains genuinely unclear after thorough investigation, report your best hypothesis with low confidence and the supporting evidence you found.

---

## Constraints
- Strictly read-only. Never attempt to modify, write, delete, move, or restart anything.
- Do not make assumptions — verify with commands.
- Do not retry a rejected command. Reason about an alternative approach.
`

var geminiExecuteSSHDeclaration = &genai.FunctionDeclaration{
	Name:        "execute_ssh",
	Description: "executes bash commands on client machines",
	Parameters: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"command": {
				Type:        genai.TypeString,
				Description: "base command name, e.g. find, ps, df, systemctl",
			},
			"flags": {
				Type:        genai.TypeArray,
				Description: "array of flags that need to be used along with the command",
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"name": {
							Type:        genai.TypeString,
							Description: "flag name",
						},
						"value": {
							Type:        genai.TypeString,
							Description: "value of flag if it takes a value",
						},
					},
					Required: []string{"name"},
				},
			},
			"positionals": {
				Type:        genai.TypeArray,
				Description: `positional arguments required for the command to execute`,
				Items: &genai.Schema{
					Type:        genai.TypeObject,
					Description: "array of positional arguments for commands.",
					Properties: map[string]*genai.Schema{
						"index": {
							Type:        genai.TypeInteger,
							Description: "positional argument index according to shell command security policy.",
						},
						"value": {
							Type:        genai.TypeString,
							Description: "positional argument satisfying shell command security policy",
						},
					},
				},
			},
			"reason": {
				Type:        genai.TypeString,
				Description: "what you know so far and why you are running this command",
			},
		},
		Required: []string{"command", "reason"},
	},
}

var tools = &genai.Tool{
	FunctionDeclarations: []*genai.FunctionDeclaration{geminiExecuteSSHDeclaration},
}

const (
	ToolExecuteSSH = "execute_ssh"
)

var reportSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"summary": {
			Type:        genai.TypeString,
			Description: "one sentence describing the exact issue",
		},
		"root_cause": {
			Type:        genai.TypeString,
			Description: "detailed explanation of the root cause issue",
		},
		"evidence": {
			Type:        genai.TypeArray,
			Description: "evidence that confirms your root cause analysis",
			Items: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"command": {
						Type:        genai.TypeString,
						Description: "command which you ran",
					},
					"observation": {
						Type:        genai.TypeString,
						Description: "result which you got which is an evidence to your root cause analysis claim",
					},
				},
				Required: []string{"command", "observation"},
			},
		},
		"recommendation": {
			Type:        genai.TypeString,
			Description: "Recommended remediation fix for the issue",
		},
		"confidence_level": {
			Type: genai.TypeString,
			Enum: reportConfidenceEnum(),
		},
		"confidence_reason": {
			Type:        genai.TypeString,
			Description: "reason justifying your confidence",
		},
	},
	Required: []string{"summary", "root_cause", "evidence", "recommendation", "confidence_level", "confidence_reason"},
}

type finalReportConfidence string

const (
	finalReportConfidenceHigh   finalReportConfidence = "High"
	finalReportConfidenceMedium finalReportConfidence = "Medium"
	finalReportConfidenceLow    finalReportConfidence = "Low"
)

func reportConfidenceEnum() []string {
	return []string{
		string(finalReportConfidenceHigh),
		string(finalReportConfidenceMedium),
		string(finalReportConfidenceLow),
	}
}

const (
	MaxGeminiTemperature = 0.2
)

type geminiConf struct {
	apiKey            string
	systemPrompt      string
	temperature       float32
	maxTokens         int32
	responseMIMEType  string
	finalReportSchema *genai.Schema
	tools             *genai.Tool
}

type gemini struct {
	model string
	geminiConf
	client *genai.Client
}

func newGemini(conf *geminiConf) (*gemini, error) {

	gemini := &gemini{
		geminiConf: *conf,
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  gemini.apiKey,
		Backend: genai.BackendGeminiAPI,
	})

	if err != nil {
		return nil, err
	}

	gemini.client = client

	return gemini, nil
}

func (g *gemini) getConf() *genai.GenerateContentConfig {
	return &genai.GenerateContentConfig{}
}

func (g *gemini) getFunctionCalls(payload *genai.GenerateContentResponse) []*genai.FunctionCall {

	var functionCalls []*genai.FunctionCall
	if len(payload.Candidates) > 0 {
		for _, p := range payload.Candidates[0].Content.Parts {
			if p.FunctionCall != nil {
				functionCalls = append(functionCalls, p.FunctionCall)
			}
		}
	}

	return functionCalls
}

func (g *gemini) generate(ctx context.Context, prompt string) (*genai.GenerateContentResponse, error) {

	promptObj := genai.Text(prompt)
	const maxAttempts = 2
	var (
		resp *genai.GenerateContentResponse
		err  error
	)

	for range maxAttempts {
		resp, err = g.client.Models.GenerateContent(ctx, g.model, promptObj, g.getConf())
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (g *gemini) getFinalReport(payload *genai.GenerateContentResponse) (*finalReport, error) {

	var finalReport finalReport
	err := json.Unmarshal([]byte(payload.Text()), &finalReport)

	if err != nil {
		return nil, err
	}

	return &finalReport, nil
}
