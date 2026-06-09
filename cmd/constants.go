package main

import "time"

const noOp = "no op"

// Transport

type pollErrKind int

const (
	pollFatalErr pollErrKind = iota
	pollLogErr
	pollClientErr
)

const (
	pollTimeout                    = 30
	clientTimeout                  = 35 * time.Second
	getMethodLimit                 = 1
	pollRetryBackoff               = 5 * time.Second
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

// Diagnoser
const (
	sshClientTimeout = 5 * time.Second
)
