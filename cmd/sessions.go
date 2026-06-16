package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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
	diagnoseMachine *struct {
		machine *machine
		query   string

		agent *agent

		loaderMsgId tgInt
		updateMsgId tgInt
		cntUpdates  int
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

	appConf *config
}

func newSession(chatId tgInt, sessionConf *tgSessionConf, conf *config) *tgSession {

	s := &tgSession{
		chatId:        chatId,
		status:        tgSessionInit,
		tgSessionConf: sessionConf,

		messageHandler: make(map[tgSessionStatus]func(context.Context, *tgMessage) error),
		cmdHandlerMap:  make(map[tgCmd]func(context.Context, *tgMessage) error),
		cbHandlerMap:   make(map[tgSessionStatus]func(context.Context, *tgCallBackQuery) error),

		lastActiveAt: time.Now(),

		appConf: conf,
	}

	s.bootstrapMsgHandler()
	s.bootstrapCmd()
	s.bootstrapCb()

	return s
}

// ------------ Bootstrap ------------
func (s *tgSession) bootstrapMsgHandler() {
	s.messageHandler[tgSessionInit] = s.handleStatusInit
	s.messageHandler[tgGetDiagnosisQuery] = s.handleStatusGetDiagnosisQuery
}

func (s *tgSession) bootstrapCmd() {
	s.cmdHandlerMap[tgCmdDiagnose] = s.cmdDiagnose
}

func (s *tgSession) bootstrapCb() {
	s.cbHandlerMap[tgSelectMachine] = s.cbSelectMachine
}

func (s *tgSession) createThread(ctx context.Context, name string) error {
	vmLock := s.transport.vmLockProvider.getLock(name)
	vmLock.Lock()
	defer vmLock.Unlock()

	threadId, err := s.transport.newThread(ctx, s.chatId, name)
	if err != nil {
		return err
	}

	s.threadId = *threadId

	s.onThreadCreated(s.chatId, s.threadId)

	return nil
}

// -------------- handle messages ------------
func (s *tgSession) msgRouter(ctx context.Context, msg *tgMessage) error {
	fn, _ := s.messageHandler[s.status]
	return fn(ctx, msg)
}

func (s *tgSession) handleStatusInit(ctx context.Context, msg *tgMessage) error {

	if len(msg.Entities) != 1 {
		err := fmt.Errorf("please use ONE of supported raven commands")
		return err
	}

	entity := msg.Entities[0]
	if entity.Type != string(tgEntityCmd) {
		err := fmt.Errorf("please use raven commands")
		return err
	}

	if entity.Offset != 0 {
		err := fmt.Errorf("please use command format : /command <arg>")
		return err
	}

	cmd := msg.Text[0:entity.Length]

	return s.cmdRouter(ctx, tgCmd(cmd), msg)
}

func (s *tgSession) handleStatusGetDiagnosisQuery(ctx context.Context, msg *tgMessage) error {
	s.data.diagnoseMachine.query = msg.Text
	s.opDiagnoseMachine(ctx)
	return nil
}

// ----------- handle commands ----------------
func (s *tgSession) cmdRouter(ctx context.Context, cmd tgCmd, msg *tgMessage) error {

	defer func() { s.lastActiveAt = time.Now() }() // check these parts

	fn, ok := s.cmdHandlerMap[cmd]
	if !ok {
		err := fmt.Errorf("command : %s not supported, please use a valid command", cmd)
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

	return s.opInitMachineDiagnosis(ctx, machine)
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

	res, err := s.transport.send(ctx, payload, tgEPSendMessage)
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
		s.data.selectMachine = nil // cleanup
		if err != nil {
			return err
		}
		return s.opInitMachineDiagnosis(ctx, machine)
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

	_, err := s.transport.send(ctx, payload, tgEPEditMessageText)
	if err != nil {
		return err
	}

	return nil
}

// ---------- Operations ------------------------
/*
 * Agent sends interim updates
 * Agent sends classified errors
 * Agent sends final response
 */

func (s *tgSession) opInitMachineDiagnosis(ctx context.Context, m *machine) error {

	err := s.createThread(ctx, m.Name)
	if err != nil {
		return err
	}

	payload := &tgSendMessage[any]{
		ChatId:   s.chatId,
		ThreadId: s.threadId,
		Text:     "What behavior would you like investigated?",
	}

	_, err = s.transport.send(ctx, payload, tgEPSendMessage)
	if err != nil {
		return err
	}

	s.status = tgGetDiagnosisQuery

	s.data.diagnoseMachine = &struct {
		machine     *machine
		query       string
		agent       *agent
		loaderMsgId tgInt
		updateMsgId tgInt
		cntUpdates  int
	}{
		machine: m,
		query:   "",
	}

	return nil
}

func (s *tgSession) opDiagnoseMachine(ctx context.Context) error {

	// TODO:errors beyond thread creation need to be handled properly, for now for the sake of simplicity we just return them

	agentConf := &agentConf{
		Machine: s.data.diagnoseMachine.machine,
		Query:   s.data.diagnoseMachine.query,
	}

	agent, err := newAgent(ctx, agentConf, s.appConf)
	if err != nil {
		return err
	}

	updCtx, cancelUpdCtx := context.WithCancel(ctx)
	defer cancelUpdCtx()

	s.data.diagnoseMachine.agent = agent
	go s.opHandleDiagnosisUpdates(updCtx)

	// TODO:handle agent result
	out, aErr := agent.run(ctx)
	if aErr != nil {
		// TODO: handle agent err
		return aErr.err
	}

	if res, ok := out.(*diagnosisResult); ok {
		cancelUpdCtx()
		//TODO: error handling
		_ = s.sendMachineDiagnosisResult(ctx, res)
		s.status = tgIdle
		s.data.diagnoseMachine = nil
	}

	return nil
}

func (s *tgSession) sendLoader() {}

func (s *tgSession) sendDiagnosisUpdate(ctx context.Context, upd string) error {

	if s.data.diagnoseMachine.cntUpdates == 0 {

		s.sendLoader()

		payload := &tgSendMessage[any]{
			ChatId:      s.chatId,
			ThreadId:    s.threadId,
			Text:        upd,
			ReplyMarkup: nil,
		}

		res, err := s.transport.send(ctx, payload, tgEPSendMessage)
		if err != nil {
			return err
		}

		s.data.diagnoseMachine.cntUpdates++
		s.data.diagnoseMachine.updateMsgId = res.Result.MessageId

		return nil
	}

	payload := &tgEditMessageText[any]{
		ChatId:      s.chatId,
		MessageId:   s.data.diagnoseMachine.updateMsgId,
		Text:        upd,
		ReplyMarkup: nil,
	}

	_, err := s.transport.send(ctx, payload, tgEPEditMessageText)
	if err != nil {
		return err
	}

	s.data.diagnoseMachine.cntUpdates++

	return nil
}

func (s *tgSession) opHandleDiagnosisUpdates(ctx context.Context) {

	a := s.data.diagnoseMachine.agent

	for {

		select {
		case upd := <-a.getUpdates():
			s.sendDiagnosisUpdate(ctx, upd)
		case <-ctx.Done():
			return
		}
	}
	//TODO: error handling
}

// TODO: Better Name this method
func (s *tgSession) sendMachineDiagnosisResult(ctx context.Context, res *diagnosisResult) error {

	reportLoc, err := generatePDF(res.Report, s.appConf)
	if err != nil {
		return err
	}

	reportPdf, err := os.Open(reportLoc)
	if err != nil {
		return err
	}
	defer os.Remove(reportLoc)

	err = s.transport.sendDocument(
		ctx,
		&tgSendDoc{
			ChatId:   s.chatId,
			ThreadId: s.threadId,
			Caption:  "Investigation report",
		},
		reportPdf,
	)
	if err != nil {
		return err
	}

	historyLoc, err := generatePDF(res.History, s.appConf)
	if err != nil {
		return err
	}

	historyPdf, err := os.Open(historyLoc)
	if err != nil {
		return err
	}
	defer os.Remove(historyLoc)

	err = s.transport.sendDocument(
		ctx,
		&tgSendDoc{
			ChatId:   s.chatId,
			ThreadId: s.threadId,
			Caption:  "Investigation history",
		},
		historyPdf,
	)
	if err != nil {
		return err
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

	_, err := s.transport.send(ctx, payload, tgEPEditMessageText)
	if err != nil {
		return err
	}

	return nil
}
