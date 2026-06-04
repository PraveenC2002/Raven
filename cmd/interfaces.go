package main

import "sync"

type Transport interface{
	send(any, string) (*tgSendMessageResponse, error)
	poll()
	newThread(tgInt, string) (*tgInt, error)
	messages() <- chan *tgMessage
	callBacks() <- chan *tgCallBackQuery
	errors() <-chan *pollErr
}

type Session interface {}

type Orchestrator interface{
	handleRequest()
	getLock() *sync.Mutex	
}

type Agent interface{}
type LLM interface {}

type Bouncer interface{
	validate(string) error
	describe() string
}

type RemoteSSH interface{
	newConn(*connectionInfo) error
	execute(string) (*sshOutput, error)
	closeConn() error
}

type Registry interface{
	initUser(*owner) error
	getUser() (*tgInt, error)
	addVm(*machine) error
	removeVm(string) error
	getVm(string) (*machine, error)
	listVm() ([]*machine, error)
	updateVm(*machine) error
}

type Auditor interface{}
