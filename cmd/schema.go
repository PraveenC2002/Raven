package main

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

type tgId int64

type request struct {
	messageId tgId
	updateId  tgId
	chatId    tgId
	userId    tgId
	query     string
}

type tgCommonResponse struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

type tgUser struct {
	Id tgId `json:"id"`
}

type tgChat struct {
	Id tgId `json:"id"`
}

type tgMessage struct {
	MessageId tgId    `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      *tgChat `json:"chat"`
	Date      int64   `json:"date"`
	Message   string  `json:"text"`
}

type tgUpdate struct {
	UpdateId tgId       `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgGetUpdateResponse struct {
	*tgCommonResponse
	Result []*tgUpdate `json:"result"`
}

type tgSendMessageResponse struct {
	*tgCommonResponse
	Result *tgMessage `json:"result"`
}

type machine struct {
	connectionInfo
	id          uuid.UUID `db:"id"`
	name        string    `db:"name"`
	description string    `db:"description"`
	createdAt   time.Time `db:"created_at"`
}

type connectionInfo struct {
	host    string `db:"host"`
	port    int    `db:"port"`
	sshUser string `db:"ssh_user"`
	keyPath string `db:"key_path"`
	hostKey string `db:"host_key"`
}

type sshOutput struct {
	output   string `db:"output"`
	exitCode int    `db:"exit_code"`
}

type owner struct {
	id      int  `db:"id"`
	ownerId tgId `db:"owner_id"`
}

type shellFlag struct {
	Name       string `toml:"name"`
	TakesVal   bool   `toml:"takesVal"`
	Glued      bool   `toml:"glued"`
	Value      string `toml:"value"`
	ValueRegex *regexp.Regexp
}

type shellPositional struct {
	Required           bool     `toml:"required"`
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
}

type shellDenyList struct {
	exact         []string `toml:"exact"`
	patterns      []string `toml:"patterns"`
	patternsRegex []*regexp.Regexp
}

type shellPolicy struct {
	Commands    []*shellCommand `toml:"commands"`
	CommandsMap map[string]*shellCommand
	denyList    *shellDenyList `toml:"denyList"`
}
