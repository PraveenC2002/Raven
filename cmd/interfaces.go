package main

import (
	"context"
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
	generate(context.Context, []*llmPart) (*llmMessage, *agentErr)
}

type Bouncer interface {
	validate(string) error
	describe() (string, error)
}

type LLMTool interface {
	toolCall(*llmFunctionCall) (*llmFunctionResponse, *agentErr)
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
