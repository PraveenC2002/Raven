package raven

import (
	"context"
)

type Raven interface {
	Run() error
}

type RavenCLI interface {
	Run() error
}

type Transport interface {
	start(ctx context.Context)
	close()
	// errors() <-chan *transportErr
}

// type Agent interface{}

type LLM interface {
	generate(context.Context, []*llmPart) (*llmMessage, *agentErr)
}

// type Bouncer interface {
// 	validate(string) error
// 	describe() (string, error)
// }

type LLMTool interface {
	setUpdateEmitter(emitUpdate func(string))
	getToolPolicy(toolName llmToolName) (string, error)
	validateFC(*remoteSSHFunctionCall) error
	callTool(context.Context, *llmFunctionCall) (*llmFunctionResponse, *agentErr)
	close() error // will always result in shutting down the system
}

// type RemoteSSH interface {
// 	execute(string) (*sshOutput, error)
// 	closeConn() error
// }

type Registry interface {
	initUser(*owner) error
	getUser() (*tgInt, error)
	addVm(*machine) error
	removeVm(string) error
	getVm(string) (*machine, error)
	listVm() ([]*machine, error)
	updateVm(*machine) error
}

// type Auditor interface{}
