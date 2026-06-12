package main

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

type tgInt int64

type request struct {
	messageId   tgInt
	updateId    tgInt
	chatId      tgInt
	userId      tgInt
	machineName string
	query       string
}

type tgCommonResponse struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

type tgUser struct {
	Id tgInt `json:"id"`
}

type tgChat struct {
	Id tgInt `json:"id"`
}

type tgMessageEntity struct {
	Type   string `json:"type"`
	Offset tgInt  `json:"offset"`
	Length tgInt  `json:"length"`
}

type tgMessage struct {
	MessageId       tgInt              `json:"message_id"`
	MessageThreadId tgInt              `json:"message_thread_id,omitempty"`
	From            *tgUser            `json:"from,omitempty"`
	Chat            *tgChat            `json:"chat"`
	Date            tgInt              `json:"date"`
	Text            string             `json:"text"`
	Entities        []*tgMessageEntity `json:"entities,omitempty"`
}

type tgCallBackQuery struct {
	Id      string     `json:"id"`
	From    *tgUser    `json:"from"`
	Message *tgMessage `json:"message,omitempty"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateId      tgInt            `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallBackQuery *tgCallBackQuery `json:"callback_query,omitempty"`
}

type tgGetUpdateResponse struct {
	*tgCommonResponse
	Result []*tgUpdate `json:"result"`
}

type tgSendMessageResponse struct {
	*tgCommonResponse
	Result *tgMessage `json:"result"`
}

type tgInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type tgInlineKeyboardMarkup struct {
	InlineKeyboard [][]*tgInlineKeyboardButton `json:"inline_keyboard"`
}

type tgSendMessage[T any] struct {
	ChatId      tgInt  `json:"chat_id"`
	ThreadId    tgInt  `json:"message_thread_id,omitempty"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup T      `json:"reply_markup,omitempty"`
}

type tgEditMessageText[T any] struct {
	ChatId      tgInt  `json:"chat_id,omitempty"`
	MessageId   tgInt  `json:"message_id,omitempty"`
	Text        string `json:"text"`
	ReplyMarkup T      `json:"reply_markup,omitempty"`
}

type machine struct {
	Id          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
	*connectionInfo
}

type connectionInfo struct {
	Host    string `db:"host"`
	Port    int    `db:"port"`
	SshUser string `db:"ssh_user"`
	KeyPath string `db:"key_path"`
	HostKey string `db:"host_key"`
}

type sshOutput struct {
	Output   string `db:"output"`
	ExitCode int    `db:"exit_code"`
}

type owner struct {
	Id      int   `db:"id"`
	OwnerId tgInt `db:"owner_id"`
}

type shellFlag struct {
	Name         string `toml:"name"`
	TakesVal     bool   `toml:"takesVal"`
	Glued        bool   `toml:"glued"`
	ValuePattern string `toml:"value"`
	ValueRegex   *regexp.Regexp
}

type shellPositional struct {
	Required           bool     `toml:"required"`
	Index              int      `toml:"index"`
	AcceptPattern      []string `toml:"acceptPattern"`
	AcceptPatternRegex []*regexp.Regexp
	RejectPattern      []string `toml:"rejectPattern"`
	RejectPatternRegex []*regexp.Regexp
	RejectList         []string `toml:"rejectList"`
}

type shellCommand struct {
	Name        string       `toml:"name"`
	Description string       `toml:"description"`
	Flags       []*shellFlag `toml:"flags"`
	FlagsMap    map[string]*shellFlag
	Positionals []*shellPositional `toml:"positionals"`
	Template    string             `toml:"template"`
}

type shellDenyList struct {
	Exact         []string `toml:"exact"`
	Patterns      []string `toml:"patterns"`
	patternsRegex []*regexp.Regexp
}

type shellPolicy struct {
	Commands    []*shellCommand `toml:"commands"`
	CommandsMap map[string]*shellCommand
	DenyList    *shellDenyList `toml:"DenyList"`
}

type role string

const (
	roleUser  = "user"
	roleModel = "model"
	roleTool  = "tool"
)

type llmFunctionCall struct {
	ID   string
	Name string
	Args map[string]any
}

type llmFunctionResponse struct {
	ID     string
	Name   string
	Result string
}

type llmPart struct {
	Text             string
	FunctionCall     *llmFunctionCall
	FunctionResponse *llmFunctionResponse
}

type llmMessage struct {
	Role  role
	Text  string
	Parts []*llmPart
}

type remoteSSHFunctionCall struct {
	Command string `json:"command"`

	Flags []*struct {
		Name  string `json:"name"`
		Value string `json:"value,omitempty"`
	} `json:"flags,omitempty"`

	Positionals []*struct {
		Index int    `json:"index"`
		Value string `json:"value"`
	} `json:"positionals,omitempty"`

	Reason string `json:"reason"`
	Update string `json:"update"`
}

type diagnosisReport struct {
	Summary   string `json:"summary"`
	RootCause string `json:"root_cause"`
	Evidence  []*struct {
		Command     string `json:"command"`
		Observation string `json:"observation"`
	} `json:"evidence"`
	Recommendation   string                `json:"recommendation"`
	Confidence       finalReportConfidence `json:"confidence"`
	ConfidenceReason string                `json:"confidence_reason"`
}

type llmToolCall struct {
	Name          string `json:"name"`
	Action        string `json:"action"`
	OutputSummary string `json:"output_summary"`
	Reasoning     string `json:"reasoning"`
	Observation   string `json:"observation"`
}

type investigationStep struct {
	StepNumber int            `json:"step_number"`
	ToolCalls  []*llmToolCall `json:"tool_calls"`
}

type investigationHistory struct {
	MachineInfo *struct {
		Name        string
		Description string
	} `json:"machineInfo"`
	Query string               `json:"query"`
	Steps []*investigationStep `json:"steps"`
}

type llmResponseErrors struct {
	textUnmarshalErr string
}

type llmResponse struct {
	FinalResponse *agentResult
	clientErrors  *llmResponseErrors
}

type agentResult struct {
	Report  *diagnosisReport      `json:"investigation_report"`
	History *investigationHistory `json:"investigation_history"`
}
