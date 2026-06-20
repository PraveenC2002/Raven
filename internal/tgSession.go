package raven

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tgSelectMachineData struct {
	machineNames      []string
	keyboardMessageId tgInt
	totalPages        int
}

type tgDiagnoseMachineData struct {
	machine *machine
	query   string

	agent *agent

	loaderMsgId tgInt
	updateMsgId tgInt
	cntUpdates  int

	cancelDiagnosis context.CancelFunc
}

type tgSessionData struct {
	selectMachine   *tgSelectMachineData
	diagnoseMachine *tgDiagnoseMachineData
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

	dataLock *sync.Mutex
	data     *tgSessionData

	messageHandler map[tgSessionStatus]func(context.Context, *tgUpdMessage) *transportErr
	cmdHandlerMap  map[tgCmd]func(context.Context, *tgUpdMessage) *transportErr // maybe change param type to str
	cbHandlerMap   map[tgSessionStatus]func(context.Context, *tgCallBackQuery) *transportErr

	lastActiveLock *sync.Mutex
	lastActiveAt   time.Time

	ravenConf *ravenConfig
}

func newSession(chatId tgInt, sessionConf *tgSessionConf, ravenConf *ravenConfig) *tgSession {

	s := &tgSession{
		chatId:        chatId,
		status:        tgSessionInit,
		tgSessionConf: sessionConf,

		messageHandler: make(map[tgSessionStatus]func(context.Context, *tgUpdMessage) *transportErr),
		cmdHandlerMap:  make(map[tgCmd]func(context.Context, *tgUpdMessage) *transportErr),
		cbHandlerMap:   make(map[tgSessionStatus]func(context.Context, *tgCallBackQuery) *transportErr),

		dataLock: &sync.Mutex{},
		data:     &tgSessionData{},

		lastActiveLock: &sync.Mutex{},
		lastActiveAt:   time.Now(),

		ravenConf: ravenConf,
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
	s.messageHandler[tgIdle] = s.handleStatusIdle
}

func (s *tgSession) bootstrapCmd() {
	s.cmdHandlerMap[tgCmdDiagnose] = s.cmdDiagnose
}

func (s *tgSession) bootstrapCb() {
	s.cbHandlerMap[tgSelectMachine] = s.cbSelectMachine
}

// -------------- handle messages ------------

func (s *tgSession) msgRouter(ctx context.Context, msg *tgUpdMessage) *transportErr {
	defer s.updateLastActive()
	fn, ok := s.messageHandler[s.status]
	if !ok {
		return nil
	}
	return fn(ctx, msg)
}

func (s *tgSession) handleStatusInit(ctx context.Context, msg *tgUpdMessage) *transportErr {

	defer s.updateLastActive()

	if len(msg.Entities) == 0 {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"session handle status init: please use ONE of supported raven commands",
			),
			sessionKey: &tgSessionKey{
				chatId:   s.chatId,
				threadId: s.threadId,
			},
			clientMsg: "please use raven commands",
		}
	}

	if len(msg.Entities) > 1 {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"session handle status init: please use ONE of supported raven commands",
			),
			sessionKey: &tgSessionKey{
				chatId:   s.chatId,
				threadId: s.threadId,
			},
			clientMsg: "please use ONE of supported raven commands",
		}
	}

	entity := msg.Entities[0]
	if entity.Type != string(tgEntityCmd) {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"session handle status init: please use raven commands",
			),
			sessionKey: &tgSessionKey{
				chatId:   s.chatId,
				threadId: s.threadId,
			},
			clientMsg: "please use raven commands",
		}
	}

	if entity.Offset != 0 {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"session handle status init: please use command format: /command <arg>",
			),
			sessionKey: &tgSessionKey{
				chatId:   s.chatId,
				threadId: s.threadId,
			},
			clientMsg: "please use command format: /command <arg>",
		}
	}

	cmd := msg.Text[1:entity.Length]

	return s.cmdRouter(ctx, tgCmd(cmd), msg)
}

func (s *tgSession) handleStatusGetDiagnosisQuery(ctx context.Context, msg *tgUpdMessage) *transportErr {
	defer s.updateLastActive()
	s.dataLock.Lock()
	s.data.diagnoseMachine.query = msg.Text
	s.dataLock.Unlock()
	return s.opDiagnoseMachine(ctx)
}

func (s *tgSession) handleStatusIdle(ctx context.Context, msg *tgUpdMessage) *transportErr {

	clientMsg := &tgNewMessage{
		reqEndpoint: tgSendNewMessageEP,
		ChatId:      msg.Chat.Id,
		ThreadId:    msg.MessageThreadId,
		Text:        "start new investigation from main chat in separate thread",
	}

	_, err := s.transport.sendMessageWithRetry(ctx, clientMsg)

	return err
}

// ----------- handle commands ----------------
func (s *tgSession) cmdRouter(ctx context.Context, cmd tgCmd, msg *tgUpdMessage) *transportErr {

	defer s.updateLastActive()

	fn, ok := s.cmdHandlerMap[cmd]
	if !ok {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"session command router: command %q not supported, please use a valid command",
				cmd,
			),
			sessionKey: &tgSessionKey{
				chatId:   s.chatId,
				threadId: s.threadId,
			},
			clientMsg: fmt.Sprintf("command %q not supported", cmd),
		}
	}

	return fn(ctx, msg)
}

// --------------- diagnose command -------------
func (s *tgSession) cmdDiagnose(ctx context.Context, msg *tgUpdMessage) *transportErr {

	defer s.updateLastActive()
	entity := msg.Entities[0]
	machineName := strings.Trim(msg.Text[entity.Length:], " ")

	machine, err := s.registry.getVm(machineName)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return &transportErr{
			kind: transportErrFatal,
			err:  fmt.Errorf("session diagnose command: get machine %q: %w", machineName, err),
		}
	}

	if err != nil && errors.Is(err, sql.ErrNoRows) {

		log.Printf("getting machine names")
		machineNames, err := s.getMachineNames()
		if err != nil {
			return err.Wrap(fmt.Sprintf("session diagnose command: get machine names after machine %q not found:", machineName))
		}
		log.Printf("got machine names")

		return s.sendNewMachineKeyboard(ctx, machineNames)
	}

	return s.opInitMachineDiagnosis(ctx, machine)
}

func (s *tgSession) getMachineNames() ([]string, *transportErr) {

	defer s.updateLastActive()

	machines, err := s.registry.listVm()
	if err != nil {
		return nil, &transportErr{
			kind: transportErrFatal,
			err:  fmt.Errorf("session get machine names: list machines: %w", err),
		}
	}

	var machineNames []string
	for _, m := range machines {
		machineNames = append(machineNames, m.Name)
	}

	return machineNames, nil
}

func (s *tgSession) sendNewMachineKeyboard(ctx context.Context, machineNames []string) *transportErr {

	defer s.updateLastActive()
	totalPages := len(machineNames) / 5
	if len(machineNames)%5 != 0 {
		totalPages++
	}

	keyboardKeys := s.utilMachinesKeyboard(machineNames[:5], 1, totalPages)

	payload := &tgNewMessage{
		reqEndpoint: tgSendNewMessageEP,
		ChatId:      s.chatId,
		Text:        "Select a virtual machine",
		ReplyMarkup: &tgInlineKeyboardMarkup{
			InlineKeyboard: keyboardKeys,
		},
	}

	log.Printf("built keyboard, sending it")

	res, err := s.transport.sendMessageWithRetry(ctx, payload)
	if err != nil {
		return err.Wrap("session send machine keyboard: send keyboard message:")
	}

	log.Printf("sent keyboard")

	s.dataLock.Lock()
	s.data.selectMachine = &tgSelectMachineData{
		machineNames:      machineNames,
		keyboardMessageId: res.Result.MessageId,
		totalPages:        totalPages,
	}
	s.dataLock.Unlock()

	s.status = tgSelectMachine

	return nil
}

// -------------- handle callbacks ---------------
func (s *tgSession) cbRouter(ctx context.Context, cb *tgCallBackQuery) *transportErr {
	defer s.updateLastActive()
	fn, ok := s.cbHandlerMap[s.status]
	if !ok {
		return nil
	}
	return fn(ctx, cb)
}

func (s *tgSession) cbSelectMachine(ctx context.Context, cb *tgCallBackQuery) *transportErr {

	defer s.updateLastActive()
	data := cb.Data

	switch {
	case data == noOp:
		return nil
	case strings.HasPrefix(data, "nav:next"):

		pageStr := strings.TrimPrefix(data, "nav:next:curr:")
		currPage, _ := strconv.Atoi(pageStr)

		s.dataLock.Lock()
		data := s.data.selectMachine
		s.dataLock.Unlock()
		start := currPage * 5
		end := min(start+5, len(data.machineNames))
		return s.editMachineKeyboard(ctx, data.machineNames[start:end], currPage+1, data.totalPages)

	case strings.HasPrefix(data, "nav:prev"):
		pageStr := strings.TrimPrefix(data, "nav:prev:curr:")
		currPage, _ := strconv.Atoi(pageStr)

		s.dataLock.Lock()
		data := s.data.selectMachine
		s.dataLock.Unlock()
		start := (currPage - 2) * 5
		end := (currPage - 1) * 5
		return s.editMachineKeyboard(ctx, data.machineNames[start:end], currPage-1, data.totalPages)

	default:

		machine, err := s.registry.getVm(data)

		if err != nil {

			if errors.Is(err, sql.ErrNoRows) {
				return &transportErr{
					kind: transportErrClient,
					err: fmt.Errorf(
						"session select machine callback: selected machine %q no longer exists",
						data,
					),
					sessionKey: &tgSessionKey{
						chatId:   s.chatId,
						threadId: s.threadId,
					},
					clientMsg: fmt.Sprintf("selected machine %q no longer exists", data),
				}
			}

			return &transportErr{
				kind: transportErrFatal,
				err: fmt.Errorf(
					"session select machine callback: get machine %q: %w",
					data,
					err,
				),
			}
		}
		return s.opInitMachineDiagnosis(ctx, machine)
	}
}

func (s *tgSession) editMachineKeyboard(ctx context.Context, machineNames []string, currPage, totalPages int) *transportErr {

	defer s.updateLastActive()

	keyboardKeys := s.utilMachinesKeyboard(machineNames, currPage, totalPages)

	s.dataLock.Lock()
	msgId := s.data.selectMachine.keyboardMessageId
	s.dataLock.Unlock()

	payload := &tgEditMessage{
		reqEndpoint: tgEditMessageReplyMarkupEP,
		ChatId:      s.chatId,
		MessageId:   msgId,
		Text:        "Select a virtual machine",
		ReplyMarkup: &tgInlineKeyboardMarkup{
			InlineKeyboard: keyboardKeys,
		},
	}

	_, err := s.transport.sendMessageWithRetry(ctx, payload)
	if err != nil {
		return err.Wrap(fmt.Sprintf("session edit machine keyboard: edit keyboard page %d/%d", currPage, totalPages))
	}

	return nil
}

// ---------- Operations ------------------------

// -------------- diagnosis op --------------------
func (s *tgSession) opInitMachineDiagnosis(ctx context.Context, m *machine) *transportErr {

	defer s.updateLastActive()

	err := s.createThread(ctx, m.Name)
	if err != nil {
		return err.Wrap(fmt.Sprintf("session init machine diagnosis: create thread for machine %q: ", m.Name))
	}

	payload := &tgNewMessage{
		reqEndpoint: tgSendNewMessageEP,
		ChatId:      s.chatId,
		ThreadId:    s.threadId,
		Text:        "What would you like investigated?",
	}

	_, err = s.transport.sendMessageWithRetry(ctx, payload)
	if err != nil {
		return err.Wrap(fmt.Sprintf("session init machine diagnosis: send investigation prompt for machine %q:", m.Name))
	}

	s.status = tgGetDiagnosisQuery

	s.dataLock.Lock()
	s.data.diagnoseMachine = &tgDiagnoseMachineData{
		machine: m,
		query:   "",
	}
	s.dataLock.Unlock()

	return nil
}

func (s *tgSession) opDiagnoseMachine(ctx context.Context) *transportErr {

	defer s.updateLastActive()

	s.dataLock.Lock()
	agentConf := &agentConf{
		Machine: s.data.diagnoseMachine.machine,
		Query:   s.data.diagnoseMachine.query,
	}
	s.dataLock.Unlock()

	sessKey := &tgSessionKey{
		chatId:   s.chatId,
		threadId: s.threadId,
	}

	agent, err := newAgent(ctx, agentConf, s.ravenConf)
	if err != nil {
		return agentToTransportErr(
			err.Wrap("create agent: "),
			sessKey,
			"failed to create agent",
		)
	}

	diagCtx, cancelDiagCtx := context.WithCancel(ctx)
	updErrCh := make(chan *transportErr)
	defer cancelDiagCtx()

	s.dataLock.Lock()
	s.data.diagnoseMachine.agent = agent
	s.data.diagnoseMachine.cancelDiagnosis = cancelDiagCtx
	s.dataLock.Unlock()

	go s.opHandleDiagnosisUpdates(diagCtx, updErrCh)

	type agentRes struct {
		out *llmFinalResponse
		err *agentErr
	}

	agentDoneCh := make(chan agentRes, 1)

	go func() {
		out, err := agent.run(diagCtx)
		agentDoneCh <- agentRes{
			out: out,
			err: err,
		}
	}()

	var (
		out *llmFinalResponse
	)

	select {
	case <-diagCtx.Done():
		return &transportErr{
			kind: transportErrTerminate,
			err:  diagCtx.Err(),
		}
	case updErr := <-updErrCh:
		return updErr
	case res := <-agentDoneCh:
		s.updateLastActive()
		out = res.out
		err = res.err
	}

	if err != nil {
		if errors.Is(err, agentErrMaxIterationsExceeded) {

			if out != nil && out.DiagnosisResult != nil {
				cancelDiagCtx()

				err := s.sendMachineDiagnosisResult(ctx, out.DiagnosisResult)
				if err != nil {
					return err.Wrap(fmt.Sprintf("send diagnosis result for machine %q:", agentConf.Machine.Name))
				}

				s.status = tgIdle
				s.dataLock.Lock()
				s.data.diagnoseMachine = nil
				s.dataLock.Unlock()
			}
			return agentToTransportErr(
				err.Wrap("run diagnosis agent:"),
				sessKey,
				"investigation stopped after reaching the maximum iteration limit; review results carefully",
			)
		}

		return agentToTransportErr(
			err.Wrap("run diagnosis agent:"),
			sessKey,
			"agent failed while running",
		)
	}

	if out != nil && out.DiagnosisResult != nil {

		cancelDiagCtx()

		err := s.sendMachineDiagnosisResult(ctx, out.DiagnosisResult)
		if err != nil {
			return err.Wrap(fmt.Sprintf("send diagnosis result for machine %q:", agentConf.Machine.Name))
		}

		s.status = tgIdle
		s.dataLock.Lock()
		s.data.diagnoseMachine = nil
		s.dataLock.Unlock()
	}

	return nil
}

func (s *tgSession) sendLoader() {
	defer s.updateLastActive()
}

func (s *tgSession) sendDiagnosisUpdate(ctx context.Context, upd string) *transportErr {

	defer s.updateLastActive()

	s.dataLock.Lock()
	if s.data.diagnoseMachine.cntUpdates == 0 {

		s.dataLock.Unlock()

		s.sendLoader()

		payload := &tgNewMessage{
			reqEndpoint: tgSendNewMessageEP,
			ChatId:      s.chatId,
			ThreadId:    s.threadId,
			Text:        upd,
			ReplyMarkup: nil,
		}

		res, err := s.transport.send(ctx, payload)
		if err != nil {
			return err.Wrap("send diagnosis update: send initial update message:")
		}

		s.dataLock.Lock()
		s.data.diagnoseMachine.cntUpdates++
		s.data.diagnoseMachine.updateMsgId = res.Result.MessageId
		s.dataLock.Unlock()

		return nil
	}

	payload := &tgEditMessage{
		reqEndpoint: tgEditMessageTextEP,
		ChatId:      s.chatId,
		MessageId:   s.data.diagnoseMachine.updateMsgId,
		Text:        upd,
	}

	s.dataLock.Unlock()
	_, err := s.transport.send(ctx, payload)

	s.dataLock.Lock()

	if err != nil {
		return err.Wrap(fmt.Sprintf("send diagnosis update: edit update message %d: ", s.data.diagnoseMachine.updateMsgId))
	}

	s.data.diagnoseMachine.cntUpdates++

	s.dataLock.Unlock()

	return nil
}

func (s *tgSession) opHandleDiagnosisUpdates(ctx context.Context, updErrCh chan<- *transportErr) {

	defer s.updateLastActive()

	s.dataLock.Lock()
	a := s.data.diagnoseMachine.agent
	s.dataLock.Unlock()

	for {

		select {
		case upd, ok := <-a.getUpdates():
			if !ok {
				return
			}
			s.updateLastActive()
			err := s.sendDiagnosisUpdate(ctx, upd)
			if err != nil && (err.kind == transportErrFatal || err.kind == transportErrTerminate) {
				updErrCh <- err
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// TODO: Better Name this method
func (s *tgSession) sendMachineDiagnosisResult(ctx context.Context, res *diagnosisResult) *transportErr {

	defer s.updateLastActive()

	type diagnosisContext struct {
		MachineName        string
		MachineDescription string
		ReportedIssue      string
	}

	type pdfTemplateData struct {
		*diagnosisContext
		*diagnosisReport
		*investigationHistory
		CSS template.CSS
	}

	s.dataLock.Lock()
	diagnosisCtx := &diagnosisContext{
		MachineName:        s.data.diagnoseMachine.machine.Name,
		MachineDescription: s.data.diagnoseMachine.machine.Description,
		ReportedIssue:      s.data.diagnoseMachine.query,
	}
	s.dataLock.Unlock()

	reportData := &pdfTemplateData{
		diagnosisContext: diagnosisCtx,
		diagnosisReport:  res.Report,
		CSS:              template.CSS(investigationReportCSS),
	}

	sessKey := &tgSessionKey{
		chatId:   s.chatId,
		threadId: s.threadId,
	}

	reportLoc, err := generatePDF(
		"investigation-report-*.html",
		investigationReportTmpl,
		reportData,
		s.ravenConf.tempDir,
	)
	if err != nil {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"send machine diagnosis result: generate investigation report pdf: %w",
				err,
			),
			sessionKey: sessKey,
			clientMsg:  "failed to generate investigation report",
		}
	}

	reportPdf, err := os.Open(reportLoc)
	if err != nil {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"send machine diagnosis result: open investigation report pdf %q: %w",
				reportLoc,
				err,
			),
			sessionKey: sessKey,
			clientMsg:  "failed to generate investigation report",
		}
	}
	defer os.Remove(reportLoc)
	defer reportPdf.Close()

	_, tErr := s.transport.sendDocWithRetry(
		ctx,
		&tgDocInfo{
			ChatId:   s.chatId,
			ThreadId: s.threadId,
			Caption:  "Investigation report",
		},
		reportPdf,
	)
	if tErr != nil {
		return tErr.Wrap("send machine diagnosis result: send investigation report pdf: ")
	}

	historyData := &pdfTemplateData{
		diagnosisContext:     diagnosisCtx,
		investigationHistory: res.History,
		CSS:                  template.CSS(investigationHistoryCSS),
	}

	historyLoc, err := generatePDF(
		"investigation-history-*.html",
		investigationHistoryTmpl,
		historyData,
		s.ravenConf.tempDir,
	)
	if err != nil {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"send machine diagnosis result: generate investigation history pdf: %w",
				err,
			),
			sessionKey: sessKey,
			clientMsg:  "failed to generate investigation history",
		}
	}

	historyPdf, err := os.Open(historyLoc)
	if err != nil {
		return &transportErr{
			kind: transportErrClient,
			err: fmt.Errorf(
				"send machine diagnosis result: open investigation history pdf %q: %w",
				historyLoc,
				err,
			),
			sessionKey: sessKey,
			clientMsg:  "failed to generate investigation history",
		}
	}
	defer os.Remove(historyLoc)
	defer historyPdf.Close()

	_, tErr = s.transport.sendDocWithRetry(
		ctx,
		&tgDocInfo{
			ChatId:   s.chatId,
			ThreadId: s.threadId,
			Caption:  "Investigation history",
		},
		historyPdf,
	)
	if tErr != nil {
		return tErr.Wrap("send machine diagnosis result: send investigation history pdf: ")
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

	defer s.updateLastActive()

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

func (s *tgSession) createThread(ctx context.Context, name string) *transportErr {

	defer s.updateLastActive()

	vmLock := s.transport.vmLockProvider.getLock(name)
	vmLock.Lock()
	defer vmLock.Unlock()

	threadId, err := s.transport.createThreadWithRetry(ctx, s.chatId, name)
	if err != nil {
		return err.Wrap(fmt.Sprintf("session create thread: create telegram thread for machine %q:", name))
	}

	s.threadId = *threadId

	s.onThreadCreated(s.chatId, s.threadId)

	return nil
}

func (s *tgSession) updateLastActive() {
	s.lastActiveLock.Lock()
	s.lastActiveAt = time.Now()
	s.lastActiveLock.Unlock()
}
