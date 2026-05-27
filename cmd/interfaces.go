package main

type Transport interface{
	send(tgId, string) error
	poll() error
	requests() <- chan *request
}

type Orchestrator interface{}
type Agent interface{}
type LLM interface {}
type Bouncer interface{}

type Diagnoser interface{
	newConn(*connectionInfo) error
	execute(string) (*diagnoseResult, error)
	closeConn() error
}

type Registry interface{
	initUser(*owner) error
	addVm(*machine) error
	removeVm(string) error
	getVm(string) (*machine, error)
	listVm() ([]*machine, error)
	updateVm(*machine) error
}

type Auditor interface{}
