package raven

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/genai"
)

var gemExecuteSSHDecl = &genai.FunctionDeclaration{
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
			"update": {
				Type: genai.TypeString,
				// TODO: Better description which enforces some kind of present tense language
				Description: "interim update describing your current state and action you are performing in 20-30 words.",
			},
		},
		Required: []string{"command", "reason", "update"},
	},
}

var gemTools = &genai.Tool{
	FunctionDeclarations: []*genai.FunctionDeclaration{gemExecuteSSHDecl},
}

var gemToolActionSchema = &genai.Schema{
	Type:        genai.TypeObject,
	Description: "action object which you recieved in function response for a function's function call",
	Properties: map[string]*genai.Schema{
		"mode": {
			Type:        genai.TypeString,
			Description: "function call execution environment",
		},
		"operation": {
			Type:        genai.TypeString,
			Description: "operation that was performed by function call",
		},
	},
}

var gemReportSchema = &genai.Schema{
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
					"action": gemToolActionSchema,
					"observation": {
						Type:        genai.TypeString,
						Description: "result which you got which is an evidence to your root cause analysis claim",
					},
				},
				Required: []string{"action", "observation"},
			},
		},
		"recommendation": {
			Type:        genai.TypeString,
			Description: "Recommended remediation for the issue",
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

type gemConfidence string

const (
	gemConfidenceHigh   gemConfidence = "HIGH"
	gemConfidenceMedium gemConfidence = "MEDIUM"
	gemConfidenceLow    gemConfidence = "LOW"
)

func reportConfidenceEnum() []string {
	return []string{
		string(gemConfidenceHigh),
		string(gemConfidenceMedium),
		string(gemConfidenceLow),
	}
}

var gemInvestigationHistorySchema = &genai.Schema{
	Description: "Chronological record of the complete investigation performed to diagnose the machine and answer the user's query.",
	Type:        genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"steps": {
			Type:        genai.TypeArray,
			Description: "Chronological sequence of investigation steps performed while diagnosing the machine.",
			Items: &genai.Schema{
				Type:        genai.TypeObject,
				Description: "A single investigation step containing one or more tool invocations.",
				Properties: map[string]*genai.Schema{
					"step_number": {
						Type:        genai.TypeString,
						Description: "Sequential step number in the investigation timeline.",
					},

					"tool_calls": {
						Type:        genai.TypeArray,
						Description: "All tool invocations performed during this investigation step.",
						Items: &genai.Schema{
							Type:        genai.TypeObject,
							Description: "Record of a single tool invocation and the reasoning behind it.",
							Properties: map[string]*genai.Schema{
								"name": {
									Type:        genai.TypeString,
									Description: "Name of the tool that was invoked.",
								},

								"action": gemToolActionSchema,

								"output_summary": {
									Type:        genai.TypeString,
									Description: "Concise summary of the tool output, highlighting only information relevant to the investigation.",
								},

								"reasoning": {
									Type:        genai.TypeString,
									Description: "Reason for performing this tool invocation/action and the information expected to be obtained.",
								},

								"observation": {
									Type:        genai.TypeString,
									Description: "Findings inferred from the tool output and how those findings influenced subsequent investigation steps.",
								},
							},
							Required: []string{
								"name",
								"action",
								"output_summary",
								"reasoning",
								"observation",
							},
						},
					},
				},
				Required: []string{
					"step_number",
					"tool_calls",
				},
			},
		},
	},
	Required: []string{
		"steps",
	},
}

var gemResponseSchema = &genai.Schema{
	Type:        genai.TypeObject,
	Description: "Agent response envelope. Exactly one response type should be present.",
	Properties: map[string]*genai.Schema{
		"final_response": {
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"diagnosis_result": {
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"investigation_report":  gemReportSchema,
						"investigation_history": gemInvestigationHistorySchema,
					},
					Required: []string{"investigation_report", "investigation_history"},
				},
			},
		},
	},
}

// gemini config
const (
	gemModel                    = "gemini-2.5-pro"
	gemMaxTemperature   float32 = 0.2
	gemMaxTokens        int32   = 8192
	gemResponseMIMEType         = "application/json"
)

type gemini struct {
	client       *genai.Client
	systemPrompt string
	history      []*genai.Content
	errDomain    string
	ravenConf    *ravenConfig
}

func newGemini(ctx context.Context, systemPrompt string, apiKey string) (*gemini, error) {

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}

	gemini := &gemini{
		client:       client,
		systemPrompt: systemPrompt,
		history:      []*genai.Content{},
		errDomain:    "llm error :",
	}

	return gemini, nil
}

func (g *gemini) getConf() *genai.GenerateContentConfig {

	sysPrompt := genai.NewContentFromText(g.systemPrompt, genai.RoleUser)

	return &genai.GenerateContentConfig{
		SystemInstruction: sysPrompt,
		Temperature:       ptr(gemMaxTemperature),
		MaxOutputTokens:   gemMaxTokens,
		ResponseMIMEType:  gemResponseMIMEType,
		ResponseSchema:    gemResponseSchema,
		Tools:             []*genai.Tool{gemTools},
	}
}

func (g *gemini) buildContent(parts []*llmPart) []*genai.Content {

	content := &genai.Content{
		Role: string(roleUser),
	}

	var geminiParts []*genai.Part

	for _, p := range parts {
		if len(p.Text) != 0 {

			part := &genai.Part{
				Text: p.Text,
			}

			geminiParts = append(geminiParts, part)
		}

		if p.FunctionResponse != nil {

			response := make(map[string]any)

			if len(p.FunctionResponse.Result) != 0 {
				response["result"] = p.FunctionResponse.Result
			} else if len(p.FunctionResponse.Error) != 0 {
				response["error"] = p.FunctionResponse.Error
			}

			if p.FunctionResponse.Action != nil {
				response["Action"] = map[string]string{
					"Mode":      p.FunctionResponse.Action.Mode,
					"Operation": p.FunctionResponse.Action.Operation,
				}
			}

			fr := &genai.FunctionResponse{
				ID:       p.FunctionResponse.ID,
				Name:     string(p.FunctionResponse.Name),
				Response: response,
			}

			part := &genai.Part{
				FunctionResponse: fr,
			}

			geminiParts = append(geminiParts, part)
		}
	}

	content.Parts = geminiParts

	return []*genai.Content{content}
}

func (g *gemini) extractLLMMessage(resp *genai.GenerateContentResponse) *llmMessage {
	var msg llmMessage

	if len(resp.Candidates) > 0 {

		msg.Role = llmRole(resp.Candidates[0].Content.Role)
		if len(resp.Text()) != 0 {
			part := llmPart{
				Text: resp.Text(),
			}
			msg.Parts = append(msg.Parts, &part)
		}

		for _, p := range resp.Candidates[0].Content.Parts {
			if p.FunctionCall != nil {

				fc := llmFunctionCall{
					ID:   p.FunctionCall.ID,
					Name: llmToolName(p.FunctionCall.Name),
					Args: p.FunctionCall.Args,
				}

				part := llmPart{
					FunctionCall: &fc,
				}

				msg.Parts = append(msg.Parts, &part)
			}
		}
	}

	return &msg
}

// gemini API error statuses
const (
	gemInvalidArgument    = "INVALID_ARGUMENT"
	gemFailedPrecondition = "FAILED_PRECONDITION"
	gemPermissionDenied   = "PERMISSION_DENIED"
	gemNotFound           = "NOT_FOUND"
	gemResourceExhausted  = "RESOURCE_EXHAUSTED"
	gemCancelled          = "CANCELLED"
	gemInternalServerErr  = "INTERNAL"
	gemUnavailable        = "UNAVAILABLE"
	gemDeadlineExceeded   = "DEADLINE_EXCEEDED"
)

func (g *gemini) handleError(err error) *agentErr {

	var apiErr *genai.APIError

	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		// Fatal
		case gemPermissionDenied, gemFailedPrecondition, gemInvalidArgument, gemNotFound:
			err = fmt.Errorf("%s %s", g.errDomain, err.Error())
			return newAgentError(agentErrFatal, err)
		// retry
		case gemResourceExhausted, gemInternalServerErr, gemUnavailable, gemDeadlineExceeded:
			err = fmt.Errorf("%s %s", g.errDomain, err.Error())
			return newAgentError(agentErrLlmRetry, err)
		default:
			err = fmt.Errorf("%s unhandled API error status: %s", g.errDomain, err.Error())
			return newAgentError(agentErrTerminate, err)
		}
	}

	return nil
}

func (g *gemini) call(ctx context.Context, contents []*genai.Content) (*genai.GenerateContentResponse, *agentErr) {

	var (
		resp *genai.GenerateContentResponse
		err  error
	)

	t := BaseRetryBackoffTime

	for i := range MaxRetry {

		resp, err = g.client.Models.GenerateContent(ctx, gemModel, contents, g.getConf())

		if err == nil {
			break
		} else {
			if agentErr := g.handleError(err); agentErr != nil && agentErr.kind == agentErrFatal {
				return nil, agentErr
			}
		}

		time.Sleep(min(t, MaxRetryTime))
		if t < MaxRetryTime {
			t = BaseRetryBackoffTime * time.Duration((1 << (i + 1)))
		}
	}

	if err != nil {
		return nil, g.handleError(err)
	}

	return resp, nil
}

const (
	gemFinishReasonStop = "STOP"
)

func (g *gemini) generate(ctx context.Context, parts []*llmPart) (*llmMessage, *agentErr) {

	var contents []*genai.Content

	if len(g.history) != 0 {
		contents = append(contents, g.history...)
	}

	contents = append(contents, g.buildContent(parts)...)

	resp, agentErr := g.call(ctx, contents)

	if agentErr != nil {
		if agentErr.kind == agentErrLlmRetry {
			return nil, newAgentError(agentErrTerminate, agentErr.err)
		}
		return nil, agentErr
	}

	g.history = contents

	if len(resp.Candidates) == 0 {
		return nil, newAgentError(agentErrTerminate, fmt.Errorf("%s no response candidates returned", g.errDomain))
	}

	if len(resp.Candidates[0].FinishReason) != 0 && resp.Candidates[0].FinishReason != gemFinishReasonStop {
		finishReason := resp.Candidates[0].FinishReason
		return nil, newAgentError(agentErrTerminate, fmt.Errorf("%s %s", g.errDomain, finishReason))
	}

	g.history = append(g.history, resp.Candidates[0].Content)

	return g.extractLLMMessage(resp), nil
}
