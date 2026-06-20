package raven

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

// telegram types
type tgInt int64

// ----------------- Receive types --------------------------

type tgUser struct {
	Id tgInt `json:"id"`
	IsBot bool `json:"is_bot"`
}

type tgChat struct {
	Id tgInt `json:"id"`
}

type tgMessageEntity struct {
	Type   string `json:"type"`
	Offset tgInt  `json:"offset"`
	Length tgInt  `json:"length"`
}

type tgUpdMessage struct {
	MessageId       tgInt              `json:"message_id"`
	MessageThreadId tgInt              `json:"message_thread_id,omitempty"`
	From            *tgUser            `json:"from,omitempty"`
	Chat            *tgChat            `json:"chat"`
	Date            tgInt              `json:"date"`
	Text            string             `json:"text"`
	Entities        []*tgMessageEntity `json:"entities,omitempty"`
}

type tgCallBackQuery struct {
	Id      string        `json:"id"`
	From    *tgUser       `json:"from"`
	Message *tgUpdMessage `json:"message,omitempty"`
	Data    string        `json:"data"`
}

type tgUpdate struct {
	UpdateId      tgInt            `json:"update_id"`
	Message       *tgUpdMessage    `json:"message,omitempty"`
	CallBackQuery *tgCallBackQuery `json:"callback_query,omitempty"`
}

type tgGetUpdateResponse struct {
	tgBaseResponse
	Result []*tgUpdate `json:"result"`
}

// --------------- Send types ----------------------------

type tgInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type tgInlineKeyboardMarkup struct {
	InlineKeyboard [][]*tgInlineKeyboardButton `json:"inline_keyboard"`
}

type tgSendRequest interface {
	endpoint() tgEndpoint
	sessionKey() *tgSessionKey
}

type tgNewMessage struct {
	reqEndpoint tgEndpoint
	ChatId      tgInt                   `json:"chat_id"`
	ThreadId    tgInt                   `json:"message_thread_id,omitempty"`
	Text        string                  `json:"text"`
	ParseMode   string                  `json:"parse_mode,omitempty"`
	ReplyMarkup *tgInlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

func (nm *tgNewMessage) endpoint() tgEndpoint {
	if nm.reqEndpoint != tgSendNewMessageEP {
		return ""
	}
	return tgSendNewMessageEP
}

func (nm *tgNewMessage) sessionKey() *tgSessionKey {
	return &tgSessionKey{
		chatId:   nm.ChatId,
		threadId: nm.ThreadId,
	}
}

type tgEditMessage struct {
	reqEndpoint tgEndpoint
	threadId    tgInt
	ChatId      tgInt                   `json:"chat_id,omitempty"`
	MessageId   tgInt                   `json:"message_id,omitempty"`
	Text        string                  `json:"text"`
	ReplyMarkup *tgInlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

func (em *tgEditMessage) endpoint() tgEndpoint {
	if em.ReplyMarkup == nil {
		if em.reqEndpoint == tgEditMessageTextEP {
			return tgEditMessageTextEP
		}
		return ""
	} else if em.reqEndpoint != tgEditMessageReplyMarkupEP {
		return ""
	}
	return tgEditMessageReplyMarkupEP
}

func (em *tgEditMessage) sessionKey() *tgSessionKey {
	return &tgSessionKey{
		chatId:   em.ChatId,
		threadId: em.threadId,
	}
}

type tgDocInfo struct {
	reqEndpoint tgEndpoint
	ChatId      tgInt  `json:"chat_id"`
	ThreadId    tgInt  `json:"message_thread_id"`
	Caption     string `json:"caption"`
}

func (di *tgDocInfo) endpoint() tgEndpoint {
	if di.reqEndpoint != tgSendDocEP {
		return ""
	}
	return tgSendDocEP
}
func (di *tgDocInfo) sessionKey() *tgSessionKey {
	return &tgSessionKey{
		chatId:   di.ChatId,
		threadId: di.ThreadId,
	}
}

type tgSendMessageResponse struct {
	tgBaseResponse
	Result *tgUpdMessage `json:"result"`
}

// ----------------- Base API interaction type --------------------------

type tgBaseResponse struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// machine types
type connectionInfo struct {
	Host    string `db:"host"`
	Port    int    `db:"port"`
	SshUser string `db:"ssh_user"`
	KeyPath string `db:"key_path"`
	HostKey string `db:"host_key"`
}

type machine struct {
	Id          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
	connectionInfo
}

// ssh types
type sshOutput struct {
	Output   string `db:"output"`
	ExitCode int    `db:"exit_code"`
}

// shell types
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

// llm types
type llmRole string

const (
	roleUser  llmRole = "user"
	roleModel llmRole = "model"
	roleTool  llmRole = "tool"
)

type llmFunctionCall struct {
	ID   string
	Name llmToolName
	Args map[string]any
}

type llmToolAction struct {
	Mode      string `json:"mode"`
	Operation string `json:"operation"`
}

type llmFunctionResponse struct {
	ID     string
	Name   llmToolName
	Action *llmToolAction
	Result string
	Error  string
}

type llmPart struct {
	Text             string
	FunctionCall     *llmFunctionCall
	FunctionResponse *llmFunctionResponse
}

type llmMessage struct {
	Role  llmRole
	Text  string
	Parts []*llmPart
}

type llmResponseErrors struct {
	textUnmarshalErr string
}

type llmFinalResponse struct {
	DiagnosisResult *diagnosisResult `json:"diagnosis_result"`
}

type llmResponse struct {
	FinalResponse *llmFinalResponse `json:"final_response"`
	clientErrors  *llmResponseErrors
}

// tool types
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

// diagnosis output types
type toolCall struct {
	Name          string         `json:"name"`
	Action        *llmToolAction `json:"action"` //TODO: handle action filling.... the actual tool action that gets executed
	OutputSummary string         `json:"output_summary"`
	Reasoning     string         `json:"reasoning"`
	Observation   string         `json:"observation"`
}

type investigationStep struct {
	StepNumber int         `json:"step_number"`
	ToolCalls  []*toolCall `json:"tool_calls"`
}

type investigationHistory struct {
	Steps []*investigationStep `json:"steps"`
}

type finalReportConfidence string

const (
	ConfidenceHigh   finalReportConfidence = "HIGH"
	ConfidenceMedium finalReportConfidence = "MEDIUM"
	ConfidenceLow    finalReportConfidence = "LOW"
)

type diagnosisReport struct {
	// *machine
	Summary   string `json:"summary"`
	RootCause string `json:"root_cause"`
	Evidence  []*struct {
		Action      *llmToolAction `json:"action"`
		Observation string         `json:"observation"`
	} `json:"evidence"`
	Recommendation   string                `json:"recommendation"`
	Confidence       finalReportConfidence `json:"confidence_level"`
	ConfidenceReason string                `json:"confidence_reason"`
}

type diagnosisResult struct {
	Report  *diagnosisReport      `json:"investigation_report"`
	History *investigationHistory `json:"investigation_history"`
}

// DB types
type owner struct {
	Id      int   `db:"id"`
	OwnerId tgInt `db:"owner_id"`
}
