package main

import "time"

// general
const noOp = "no op"

// networking

const (
	BaseRetryBackoffTime = 200 * time.Millisecond
	MaxRetry             = 10
	MaxRetryTime         = 1000 * 10 * time.Millisecond
)

// Transport

type transportErr struct {
	kind   transportErrKind
	err    error
	chatId tgInt
}

type transportErrKind int

const (
	transportErrFatal transportErrKind = iota
	transportErrLog
	transportErrClient
)

const (
	pollTimeout                    = 30
	clientTimeout                  = 35 * time.Second
	getMethodLimit                 = 1
	pollRetryBackoff               = 5 * time.Second // TODO: Implement exponential backoff on tg transport
	cmd                            = "command"
	cmdDiagnose                    = "diagnose"
	endpointGetUpdate              = "getUpdate"
	endpointCreateThread           = "createForumTopic"
	endpointSendMessage            = "sendMessage"
	endpointEditMessageText        = "editMessageText"
	endpointEditMessageReplyMarkup = "editMessageReplyMarkup"
)

// orchestrator

const (
	pruneInterval = 5 * time.Second
	sessionExpiry = 5 * 60 * time.Second
)

// Session
type sessionStatus string

const (
	awaitingMachineSelection sessionStatus = "awaitingMachineSelection"
	diagnosing               sessionStatus = "diagnosing"
)

// remoteSSH
const (
	sshClientTimeout = 5 * time.Second
)

// agent

type agentError int

const (
	agentErrFatal agentError = iota
	agentErrTerminate
	agentErrLlmRetry
)

const (
	ToolExecuteSSH = "execute_ssh"
)
