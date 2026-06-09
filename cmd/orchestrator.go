package main

import (
	"log"
	"sync"
	"time"
)

type sessionKey struct {
	chatId   tgInt
	threadId tgInt
}

type orchestrator struct {
	registry  Registry
	transport Transport

	sessionMapLock *sync.Mutex
	sessionMap     map[sessionKey]*session

	vmLock *sync.Mutex
	vmMap  map[string]*sync.Mutex
}

func newOrchestrator(transport Transport, registry Registry) *orchestrator {

	orc := &orchestrator{
		registry:       registry,
		transport:      transport,

		sessionMapLock: &sync.Mutex{},
		sessionMap:     make(map[sessionKey]*session),

		vmLock:         &sync.Mutex{},
		vmMap:          make(map[string]*sync.Mutex),
	}
	return orc
}

func (o *orchestrator) bootstrap() error {

	machines, err := o.registry.listVm()
	if err != nil {
		return err
	}

	for _, m := range machines {
		o.vmMap[m.Name] = &sync.Mutex{}
	}

	return nil
}

// TODO : trigger bootstrap when a new machine is added through the cli while raven daemon is running
func (o *orchestrator) run() error {

	if err := o.bootstrap(); err != nil {
		return err
	}

	go func() {
		o.transport.poll()
	}()

	go o.pruneSession()

	msgCh := o.transport.messages()
	callBackCh := o.transport.callBacks()
	pollErrCh := o.transport.errors()

	for {

		select {

		/* so for the scenario of race between :
		 * routines of message handlers and callback handlers trying to work with same session
		 * you can only fire a call back handler routine after the handle message routine is done with it's job
		 */
		case req := <-msgCh:
			go o.handleMessage(req)

		case cb := <-callBackCh:
			go o.handleCallback(cb)

		case pollErr := <-pollErrCh:

			switch {
			case pollErr.kind == pollFatalErr:
				return pollErr.err
			case pollErr.kind == pollLogErr:
				log.Println(pollErr.err.Error())
			case pollErr.kind == pollClientErr:
				res := &tgSendMessage[any]{
					ChatId: pollErr.chatId,
					Text:   pollErr.err.Error(),
				}
				go o.transport.send(res, endpointSendMessage)
			}
		}
	}
}

func (o *orchestrator) pruneSession() {

	for {

		time.Sleep(pruneInterval)
		o.sessionMapLock.Lock()
		for sKey, s := range o.sessionMap {
			if s.status == awaitingMachineSelection && time.Now().After(s.lastActiveAt.Add(sessionExpiry)) {
				delete(o.sessionMap, sKey)
				go s.removeMachineSelectionKeyboard()
			}
		}
		o.sessionMapLock.Unlock()
	}
}

func (o *orchestrator) handleCallback(cb *tgCallBackQuery) error {

	o.sessionMapLock.Lock()
	defer o.sessionMapLock.Unlock()

	sessionKey := sessionKey{
		chatId: cb.Message.Chat.Id,
	}

	// session key is wrong when callbacks come after thread creation

	session := o.sessionMap[sessionKey]

	return session.cbRouter(cb)
}

// TODO:Vm lock aquisition
func (o *orchestrator) handleMessage(msg *tgMessage) error {

	getVmLock := func(machineName string) *sync.Mutex {
		o.vmLock.Lock()
		defer o.vmLock.Unlock()

		return o.vmMap[machineName]
	}

	onThreadCreated := func(chatId, threadId tgInt) {
		o.sessionMapLock.Lock()
		defer o.sessionMapLock.Unlock()

		oldKey := sessionKey{
			chatId:   chatId,
			threadId: 0,
		}
		session := o.sessionMap[oldKey]
		delete(o.sessionMap, oldKey)

		newKey := sessionKey{
			chatId:   chatId,
			threadId: threadId,
		}

		o.sessionMap[newKey] = session
	}

	conf := &sessionConf{
		transport:       o.transport,
		registry:        o.registry,
		getVmLock:       getVmLock,
		onThreadCreated: onThreadCreated,
	}

	session := newSession(msg.Chat.Id, conf)

	o.sessionMapLock.Lock()
	sessionKey := sessionKey{
		chatId:   session.chatId,
		threadId: 0,
	}
	o.sessionMap[sessionKey] = session
	o.sessionMapLock.Unlock()

	return session.cmdRouter(msg)
}
