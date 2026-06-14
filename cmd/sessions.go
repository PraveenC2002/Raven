package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// why does session need transport and registry ?

type tgSessionData struct {
	selectMachine *struct {
		machineNames      []string
		keyboardMessageId tgInt
		totalPages        int
	}
}

type tgSessionConf struct {
	registry        Registry
	transport       *tgTransport
	onThreadCreated func(tgInt, tgInt)
}

type tgSession struct {
	chatId   tgInt
	threadId tgInt
	status   tgSessionStatus

	*tgSessionConf

	data *tgSessionData

	messageHandler map[tgSessionStatus]func(context.Context, *tgMessage) error
	cmdHandlerMap  map[tgCmd]func(context.Context, *tgMessage) error // maybe change param type to str
	cbHandlerMap   map[tgSessionStatus]func(context.Context, *tgCallBackQuery) error

	lastActiveAt time.Time
}

func newSession(chatId tgInt, conf *tgSessionConf) *tgSession {

	s := &tgSession{
		chatId:        chatId,
		status:        tgSessionInit,
		tgSessionConf: conf,

		messageHandler: make(map[tgSessionStatus]func(context.Context, *tgMessage) error),
		cmdHandlerMap:  make(map[tgCmd]func(context.Context, *tgMessage) error),
		cbHandlerMap:   make(map[tgSessionStatus]func(context.Context, *tgCallBackQuery) error),

		lastActiveAt: time.Now(),
	}

	s.bootstrapMsgHandler()
	s.bootstrapCmd()
	s.bootstrapCb()

	return s
}

// ------------ Bootstrap ------------
func (s *tgSession) bootstrapMsgHandler() {
	s.messageHandler[tgSessionInit] = s.handleStatusInit
}

func (s *tgSession) bootstrapCmd() {
	s.cmdHandlerMap[tgCmdDiagnose] = s.cmdDiagnose
}

func (s *tgSession) bootstrapCb() {
	s.cbHandlerMap[tgSelectMachine] = s.cbSelectMachine
}

// -------------- handle messages ------------
func (s *tgSession) msgRouter(ctx context.Context, msg *tgMessage) error {
	fn, _ := s.messageHandler[s.status]
	return fn(ctx, msg)
}

func (s *tgSession) handleStatusInit(ctx context.Context, msg *tgMessage) error {

	if len(msg.Entities) != 1 {
		err := fmt.Errorf("%s please use ONE of supported raven commands")
		return err
	}

	entity := msg.Entities[0]
	if entity.Type != string(tgEntityCmd) {
		err := fmt.Errorf("%s please use raven commands")
		return err
	}

	if entity.Offset != 0 {
		err := fmt.Errorf("%s please use command format : /command <arg>")
		return err
	}

	cmd := msg.Text[0:entity.Length]

	return s.cmdRouter(ctx, tgCmd(cmd), msg)
}

// ----------- handle commands ----------------
func (s *tgSession) cmdRouter(ctx context.Context, cmd tgCmd, msg *tgMessage) error {

	defer func() { s.lastActiveAt = time.Now() }() // check these parts

	fn, ok := s.cmdHandlerMap[cmd]
	if !ok {
		err := fmt.Errorf("%s command : %s not supported, please use a valid command", cmd)
		return err
	}

	return fn(ctx, msg)
}

// --------------- diagnose command -------------
func (s *tgSession) cmdDiagnose(ctx context.Context, msg *tgMessage) error {

	entity := msg.Entities[0]
	machineName := strings.Trim(msg.Text[entity.Length:], " ")

	machine, err := s.registry.getVm(machineName)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if err != nil && errors.Is(err, sql.ErrNoRows) {

		machineNames, err := s.getMachineNames()
		if err != nil {
			return err
		}

		return s.sendNewMachineKeyboard(ctx, machineNames)
	}

	return s.opDiagnoseMachine(ctx, machine)
}

func (s *tgSession) getMachineNames() ([]string, error) {

	machines, err := s.registry.listVm()
	if err != nil {
		return nil, err
	}

	var machineNames []string
	for _, m := range machines {
		machineNames = append(machineNames, m.Name)
	}

	return machineNames, nil
}

func (s *tgSession) sendNewMachineKeyboard(ctx context.Context, machineNames []string) error {

	totalPages := len(machineNames) / 5
	if len(machineNames)%5 != 0 {
		totalPages++
	}

	keyboardKeys := s.utilMachinesKeyboard(machineNames, 1, totalPages)

	payload := &tgSendMessage[any]{
		ChatId: s.chatId,
		Text:   "Select a virtual machine",
		ReplyMarkup: &tgInlineKeyboardMarkup{
			InlineKeyboard: keyboardKeys,
		},
	}

	res, err := s.transport.send(ctx, payload, endpointSendMessage)
	if err != nil {
		return err
	}

	s.data.selectMachine = &struct {
		machineNames      []string
		keyboardMessageId tgInt
		totalPages        int
	}{
		machineNames:      machineNames,
		keyboardMessageId: res.Result.MessageId,
		totalPages:        totalPages,
	}

	s.status = tgSelectMachine

	return nil
}

// -------------- handle callbacks ---------------
func (s *tgSession) cbRouter(ctx context.Context, cb *tgCallBackQuery) error {

	defer func() { s.lastActiveAt = time.Now() }()
	fn, _ := s.cbHandlerMap[s.status]

	return fn(ctx, cb)
}

func (s *tgSession) cbSelectMachine(ctx context.Context, cb *tgCallBackQuery) error {

	data := cb.Data

	switch {
	case strings.HasPrefix(data, "nav:next"):

		pageStr := strings.TrimPrefix(data, "nav:next:curr:")
		currPage, _ := strconv.Atoi(pageStr)

		data := s.data.selectMachine
		start := currPage * 5
		end := min(start+5, len(data.machineNames))
		return s.editMachineKeyboard(ctx, data.machineNames[start:end], currPage+1, data.totalPages)

	case strings.HasPrefix(data, "nav:prev"):
		pageStr := strings.TrimPrefix(data, "nav:prev:curr:")
		currPage, _ := strconv.Atoi(pageStr)

		data := s.data.selectMachine
		start := (currPage - 2) * 5
		end := (currPage - 1) * 5
		return s.editMachineKeyboard(ctx, data.machineNames[start:end], currPage-1, data.totalPages)

	default:
		machine, err := s.registry.getVm(data)
		if err != nil {
			return err
		}
		return s.opDiagnoseMachine(ctx, machine)
	}
}

func (s *tgSession) editMachineKeyboard(ctx context.Context, machineNames []string, currPage, totalPages int) error {

	keyboardKeys := s.utilMachinesKeyboard(machineNames, currPage, totalPages)

	msgId := s.data.selectMachine.keyboardMessageId

	payload := &tgEditMessageText[any]{
		ChatId:    s.chatId,
		MessageId: msgId,
		Text:      "Select a virtual machine",
		ReplyMarkup: &tgInlineKeyboardMarkup{
			InlineKeyboard: keyboardKeys,
		},
	}

	_, err := s.transport.send(ctx, payload, endpointEditMessageText)
	if err != nil {
		return err
	}

	return nil
}

// ---------- Operations ------------------------
/*
 * Agent asks for something (query)
 * Agent sends interim updates
 * Agent sends classified errors
 * Agent sends final response
 */
func (s *tgSession) opDiagnoseMachine(ctx context.Context, m *machine) error {

	vmLock := s.transport.vmLockProvider.getLock(m.Name)
	vmLock.Lock()
	defer vmLock.Unlock()

	threadId, err := s.transport.newThread(ctx, s.chatId, m.Name)
	if err != nil {
		return err
	}

	s.threadId = *threadId

	s.onThreadCreated(s.chatId, s.threadId)

	// TODO:errors beyond thread creation need to be handled properly, for now for the sake of simplicity we just return them

	agentConf := &agentConf{
		Machine: m,
		Query:   "stubbed", // TODO:get query from user
	}
	// TODO:stubbed ctx
	a, err := newAgent(context.Background(), agentConf)
	if err != nil {
		return err
	}

	// TODO:handle agent result
	_, aErr := a.run(context.Background())
	if aErr != nil {
		// TODO: handle agent err
		return aErr.err
	}

	return nil
}

// ------------------- utils ----------------------
func (s *tgSession) utilCreateKeyboardButton(text, data string) *tgInlineKeyboardButton {
	return &tgInlineKeyboardButton{
		Text:         text,
		CallbackData: data,
	}
}

func (s *tgSession) utilMachinesKeyboard(machinesNames []string, currPage, totalPages int) [][]*tgInlineKeyboardButton {

	var keyboard [][]*tgInlineKeyboardButton

	for _, m := range machinesNames {
		button := s.utilCreateKeyboardButton(m, m)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{button})
	}

	pageText := strconv.Itoa(currPage) + "/" + strconv.Itoa(totalPages)
	pageButton := s.utilCreateKeyboardButton(pageText, noOp)
	navText := ":curr:" + strconv.Itoa(currPage)

	if currPage == 1 {
		nextButton := s.utilCreateKeyboardButton("next", "nav:next"+navText)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{pageButton, nextButton})
	} else if currPage == totalPages {
		prevButton := s.utilCreateKeyboardButton("prev", "nav:prev"+navText)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{prevButton, pageButton})
	} else {
		nextButton := s.utilCreateKeyboardButton("next", "nav:next"+navText)
		prevButton := s.utilCreateKeyboardButton("prev", "nav:prev"+navText)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{prevButton, pageButton, nextButton})
	}

	return keyboard
}

func (s *tgSession) removeMachineSelectionKeyboard(ctx context.Context) error {

	data := s.data.selectMachine
	msgId := data.keyboardMessageId
	payload := tgEditMessageText[any]{
		ChatId:    s.chatId,
		MessageId: msgId,
		Text:      "No VM selected",
	}

	_, err := s.transport.send(ctx, payload, endpointEditMessageText)
	if err != nil {
		return err
	}

	return nil
}
