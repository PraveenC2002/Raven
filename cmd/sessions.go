package main

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sessionConf struct {
	registry        Registry
	transport       Transport
	getVmLock       func(string) *sync.Mutex
	onThreadCreated func(tgInt, tgInt)
}

type session struct {
	chatId   tgInt
	threadId tgInt
	status   sessionStatus

	state         any
	cmdHandlerMap map[string]func(*tgMessage) error
	cbHandlerMap  map[sessionStatus]func(*tgCallBackQuery) error

	lastActiveAt time.Time

	*sessionConf
}

func newSession(chatId tgInt, conf *sessionConf) *session {

	s := &session{
		chatId:        chatId,
		status:        awaitingMachineSelection,
		cmdHandlerMap: make(map[string]func(*tgMessage) error),
		cbHandlerMap:  make(map[sessionStatus]func(*tgCallBackQuery) error),
		sessionConf:   conf,
		lastActiveAt:     time.Now(),
	}

	s.bootstrapCmd()
	s.bootstrapCb()

	return s
}

func (s *session) bootstrapCmd() {
	s.cmdHandlerMap[cmdDiagnose] = s.cmdDiagnose
}

func (s *session) bootstrapCb() {
	s.cbHandlerMap[awaitingMachineSelection] = s.cbMachineSelection
}

func (s *session) cmdRouter(msg *tgMessage) error {
	defer func () { s.lastActiveAt = time.Now() }()
	
	cmd := msg.Text[:msg.Entities[0].Length]
	fn, _ := s.cmdHandlerMap[cmd]
	return fn(msg)
}

func (s *session) cbRouter(cb *tgCallBackQuery) error {

	defer func () { s.lastActiveAt = time.Now() }()
	fn, _ := s.cbHandlerMap[s.status]

	return fn(cb)
}

type awaitMachineSelectionState struct {
	machineNames      []string
	keyboardMessageId tgInt
	totalPages int
}

func (s *session) cbMachineSelection(cb *tgCallBackQuery) error {

	data := cb.Data

	switch {
	case strings.HasPrefix(data, "nav:next"):

		pageStr := strings.TrimPrefix(data, "nav:next:curr:")
		currPage, _ := strconv.Atoi(pageStr)

		if state, ok := s.state.(awaitMachineSelectionState); ok {
			start := currPage * 5
			end := min(start+5, len(state.machineNames))
			return s.editMachineKeyboard(state.machineNames[start:end], currPage + 1, state.totalPages)
		}

	case strings.HasPrefix(data, "nav:prev"):
		pageStr := strings.TrimPrefix(data, "nav:prev:curr:")
		currPage, _ := strconv.Atoi(pageStr)

		if state, ok := s.state.(awaitMachineSelectionState); ok {
			start := (currPage - 2) * 5
			end := (currPage - 1) * 5
			return s.editMachineKeyboard(state.machineNames[start:end], currPage - 1, state.totalPages)
		}

	default:
		machine, err := s.registry.getVm(data)
		if err != nil {
			return err
		}
		return s.diagnoseMachine(machine)
	}

	return nil
}

func (s *session) diagnoseMachine(m *machine) error {

	if state, ok := s.state.(awaitMachineSelectionState); ok {
		payload := tgEditMessageText[any]{
			ChatId:    s.chatId,
			MessageId: state.keyboardMessageId,
			Text:      "Starting machine " + m.Name + " diagnosis.",
		}
		_, err := s.transport.send(payload, endpointEditMessageText)
		if err != nil {
			return err
		}
	}

	vmLock := s.getVmLock(m.Name)
	vmLock.Lock()
	defer vmLock.Unlock()

	threadId, err := s.transport.newThread(s.chatId, m.Name)
	if err != nil {
		return err
	}

	s.threadId = *threadId

	s.onThreadCreated(s.chatId, s.threadId)

	return nil
}

func (s *session) cmdDiagnose(msg *tgMessage) error {

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

		return s.sendNewMachineKeyboard(machineNames)
	}

	return s.diagnoseMachine(machine)
}

func (s *session) editMachineKeyboard(machineNames []string, currPage, totalPages int) error {

	keyboardKeys := s.machinesKeyboard(machineNames, currPage, totalPages)

	var msgId tgInt
	if state, ok := s.state.(awaitMachineSelectionState); ok {
		msgId = state.keyboardMessageId
	}
	
	payload := &tgEditMessageText[any]{
		ChatId: s.chatId,
		MessageId: msgId,
		Text:   "Select a virtual machine",
		ReplyMarkup: &tgInlineKeyboardMarkup{
			InlineKeyboard: keyboardKeys,
		},
	}

	_, err := s.transport.send(payload, endpointEditMessageText)
	if err != nil {
		return err
	}

	return err
}

func (s *session) sendNewMachineKeyboard(machineNames []string) error {

	totalPages := len(machineNames) / 5
	if len(machineNames)%5 != 0 {
		totalPages++
	}

	keyboardKeys := s.machinesKeyboard(machineNames, 1, totalPages)

	payload := &tgSendMessage[any]{
		ChatId: s.chatId,
		Text:   "Select a virtual machine",
		ReplyMarkup: &tgInlineKeyboardMarkup{
			InlineKeyboard: keyboardKeys,
		},
	}

	res, err := s.transport.send(payload, endpointSendMessage)
	if err != nil {
		return err
	}

	s.state = awaitMachineSelectionState {
		machineNames:      machineNames,
		keyboardMessageId: res.Result.MessageId,
		totalPages: totalPages,
	}

	return err
}

func (s *session) createKeyboardButton(text, data string) *tgInlineKeyboardButton {
	return &tgInlineKeyboardButton{
		Text:         text,
		CallbackData: data,
	}
}

func (s *session) machinesKeyboard(machinesNames []string, currPage, totalPages int) [][]*tgInlineKeyboardButton {

	var keyboard [][]*tgInlineKeyboardButton

	for _, m := range machinesNames {
		button := s.createKeyboardButton(m, m)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{button})
	}

	pageText := strconv.Itoa(currPage) + "/" + strconv.Itoa(totalPages)
	pageButton := s.createKeyboardButton(pageText, noOp)
	navText := ":curr:" + strconv.Itoa(currPage)

	if currPage == 1 {
		nextButton := s.createKeyboardButton("next", "nav:next"+navText)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{pageButton, nextButton})
	} else if currPage == totalPages {
		prevButton := s.createKeyboardButton("prev", "nav:prev"+navText)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{prevButton, pageButton})
	} else {
		nextButton := s.createKeyboardButton("next", "nav:next"+navText)
		prevButton := s.createKeyboardButton("prev", "nav:prev"+navText)
		keyboard = append(keyboard, []*tgInlineKeyboardButton{prevButton, pageButton, nextButton})
	}

	return keyboard
}

func (s *session) removeMachineSelectionKeyboard() error {
	if state, ok := s.state.(awaitMachineSelectionState); ok {
		msgId := state.keyboardMessageId
		payload := tgEditMessageText[any]{
			ChatId:    s.chatId,
			MessageId: msgId,
			Text:      "No VM selected",
		}
		_, err := s.transport.send(payload, endpointEditMessageText)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *session) getMachineNames() ([]string, error) {

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
