package raven

import "time"

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

// tg transport

type tgAPIUrltype string

const (
	tgAPIUrl tgAPIUrltype = "https://api.telegram.org/bot"
)

const (
	pollTimeout      = 30
	clientTimeout    = 35 * time.Second
	getMethodLimit   = 1
	pollRetryBackoff = 5 * time.Second // TODO: Implement exponential backoff on tg transport
)

type tgEndpoint string

const (
	tgEPGetUpdate              tgEndpoint = "getUpdate"
	tgEPCreateThread           tgEndpoint = "createForumTopic"
	tgEPSendMessage            tgEndpoint = "sendMessage"
	tgEPSendDoc                tgEndpoint = "sendDocument"
	tgEPEditMessageText        tgEndpoint = "editMessageText"
	tgEPEditMessageReplyMarkup tgEndpoint = "editMessageReplyMarkup"
)

const (
	pruneInterval = 5 * time.Second
	sessionExpiry = 5 * 60 * time.Second
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
	tgEntityCmd tgEntity = "command"
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

type llmToolName string

const (
	ToolExecuteSSH llmToolName = "execute_ssh"
)

type llmToolMode string

const (
	llmTMShell llmToolMode = "shell"
)
