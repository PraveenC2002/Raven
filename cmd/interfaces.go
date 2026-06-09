package main

import (
	"context"

	"google.golang.org/genai"
)

type Transport interface {
	poll()
	newThread(tgInt, string) (*tgInt, error)
	messages() <-chan *tgMessage
	callBacks() <-chan *tgCallBackQuery
	send(any, string) (*tgSendMessageResponse, error)
	errors() <-chan *pollErr
}

type Session interface{}

type Orchestrator interface {
	run()
}

type Agent interface{}

type LLM interface {
	generate(context.Context, string) (*genai.GenerateContentResponse, error)
	getFunctionCalls(*genai.GenerateContentResponse) []*genai.FunctionCall
	getFinalReport(payload *genai.GenerateContentResponse) (*finalReport, error)
}

type Bouncer interface {
	validate(string) error
	describe() (string, error)
}

type LLMTool interface {
	toolCall(*genai.FunctionCall) (any, error)
}

type RemoteSSH interface {
	execute(string) (*sshOutput, error)
	closeConn() error
}

type Registry interface {
	initUser(*owner) error
	getUser() (*tgInt, error)
	addVm(*machine) error
	removeVm(string) error
	getVm(string) (*machine, error)
	listVm() ([]*machine, error)
	updateVm(*machine) error
}

type Auditor interface{}
