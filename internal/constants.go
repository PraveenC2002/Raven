package raven

import (
	"errors"
	"time"
)

// general

type dockerContainerName string

const (
	ravenContainer dockerContainerName = "raven"
)

type dockerImageRef string

const (
	ravenImageAddr dockerImageRef = "" // TODO
)

const (
	noOp = "no op"
)

// networking
const (
	BaseRetryBackoffTime = 200 * time.Millisecond
	MaxRetry             = 5
	MaxRetryTime         = 1000 * 10 * time.Millisecond
)

// Transport

type transportErrKind int

const (
	transportErrFatal transportErrKind = iota
	transportErrTerminate
	transportErrClient
	transportErrRetry
)

// tg transport

type tgAPIURLType string

const (
	tgAPIUrl tgAPIURLType = "https://api.telegram.org/bot"
)

const (
	pollTimeout      = 30
	clientTimeout    = 180 * time.Second
	getMethodLimit   = 1
	pollRetryBackoff = 5 * time.Second // TODO: Implement exponential backoff on tg transport
)

type tgEndpoint string

const (
	tgGetUpdateEP              tgEndpoint = "getUpdates"
	tgCreateThreadEP           tgEndpoint = "createForumTopic"
	tgSendNewMessageEP         tgEndpoint = "sendMessage"
	tgSendDocEP                tgEndpoint = "sendDocument"
	tgEditMessageTextEP        tgEndpoint = "editMessageText"
	tgEditMessageReplyMarkupEP tgEndpoint = "editMessageReplyMarkup"
)

const (
	pruneInterval = 20 * time.Second
	sessionExpiry = 3 * 60 * time.Second
)

// Session
type tgSessionStatus int

const (
	tgIdle tgSessionStatus = iota
	tgSessionInit
	tgSelectMachine
	tgGetDiagnosisQuery
	tgDiagnoseMachine
)

type tgEntity string

const (
	tgEntityCmd tgEntity = "bot_command"
)

type tgCmd string

const (
	tgCmdDiagnose tgCmd = "diagnose"
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
	agentMaxIterations = 15
)

var agentErrMaxIterationsExceeded = errors.New("maximum iterations exceeded")

type llmToolName string

const (
	ToolExecuteSSH llmToolName = "execute_ssh"
)

type llmToolMode string

const (
	llmTMShell llmToolMode = "shell"
)
