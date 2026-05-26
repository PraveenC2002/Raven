package main

import (
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

type diagnoseResult struct {
	stdout string `db:"stdout"`
	stderr string `db:"stderr"`
	exitCode int `db:"exit_code"`
}
