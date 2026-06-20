package raven

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"
)

type tgSessionKey struct {
	chatId   tgInt
	threadId tgInt
}

type tgTransportConf struct {
	userId         tgInt
	botToken       string
	vmLockProvider *vmLockProvider
	registry       Registry
}

type tgTransport struct {
	*tgTransportConf

	client *http.Client
	offset tgInt

	sessionMapLock *sync.Mutex
	sessionMap     map[tgSessionKey]*tgSession

	messageCh     chan *tgUpdMessage
	callBackCh    chan *tgCallBackQuery
	tranportErrCh chan *transportErr

	ravenConf *ravenConfig

	closeOnce sync.Once
}

func newTgTransport(transporConf *tgTransportConf, ravenConf *ravenConfig) *tgTransport {

	t := &tgTransport{

		tgTransportConf: transporConf,
		client: &http.Client{
			Timeout: clientTimeout,
		},
		offset: 0,

		sessionMapLock: &sync.Mutex{},
		sessionMap:     make(map[tgSessionKey]*tgSession),

		messageCh:     make(chan *tgUpdMessage, 20),
		callBackCh:    make(chan *tgCallBackQuery, 20),
		tranportErrCh: make(chan *transportErr, 20),

		ravenConf: ravenConf,
	}

	return t
}

func (t *tgTransport) authenticate(userId tgInt) error {

	if userId != t.userId {
		return fmt.Errorf(
			"transport authentication: unauthorized user id %d",
			userId,
		)
	}

	return nil
}

func (t *tgTransport) buildUrl(ep tgEndpoint) string {
	return string(tgAPIUrl) + t.botToken + "/" + string(ep)
}

func (t *tgTransport) pushToChannel(ctx context.Context, payload any) {
	switch v := payload.(type) {
	case *tgUpdMessage:
		select {
		case t.messageCh <- v:
		case <-ctx.Done():
		}
	case *tgCallBackQuery:
		select {
		case t.callBackCh <- v:
		case <-ctx.Done():
		}
	case *transportErr:
		select {
		case t.tranportErrCh <- v:
		case <-ctx.Done():
		}
	}
}

func (t *tgTransport) poll(ctx context.Context) {

	for {

		params := url.Values{}
		params.Set("offset", strconv.Itoa(int(t.offset)))
		params.Set("timeout", strconv.Itoa(pollTimeout))
		params.Set("limit", strconv.Itoa(getMethodLimit))
		params.Set("allowed_updates", `["message", "callback_query"]`)

		reqUrl := t.buildUrl(tgGetUpdateEP) + "?" + params.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqUrl, nil)
		if err != nil {
			t.pushToChannel(
				ctx,
				newTransportErr(
					transportErrFatal,
					fmt.Errorf("transport poll: create getUpdates request: %w", err),
					nil,
				),
			)
			return
		}

		res, err := t.client.Do(req)
		if err != nil {

			if ctx.Err() != nil { // TODO: check this, we normally return nil on context cancellation
				// t.pushToChannel(
				// 	ctx,
				// 	newTransportErr(
				// 		transportErrTerminate,
				// 		fmt.Errorf("transport poll: %w", ctx.Err()),
				// 		nil,
				// 	),
				// )
				return
			} else {
				time.Sleep(pollRetryBackoff) // backoff
				continue
			}
		}

		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			continue
		}

		var resp tgGetUpdateResponse
		err = json.Unmarshal(body, &resp)
		if err != nil {
			t.pushToChannel(
				ctx,
				newTransportErr(
					transportErrTerminate,
					fmt.Errorf("transport poll: unmarshal getUpdates response: %w", err),
					nil,
				),
			)
			return
		}

		if !resp.Ok {
			time.Sleep(pollRetryBackoff)
			continue
		}

		if len(resp.Result) > 0 {

			t.offset = resp.Result[len(resp.Result)-1].UpdateId + 1

			for _, upd := range resp.Result {

				switch {

				case upd.Message != nil:

					msg := upd.Message
					if err := t.authenticate(msg.From.Id); err != nil {
						continue
					}

					t.pushToChannel(ctx, msg)

				case upd.CallBackQuery != nil:

					cb := upd.CallBackQuery

					if err := t.authenticate(cb.From.Id); err != nil {
						continue
					}

					t.pushToChannel(ctx, cb)
				}
			}
		}
	}
}

func (t *tgTransport) doRequest(ctx context.Context, req *http.Request, out any) *transportErr {

	res, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return newTransportErr(
				transportErrTerminate,
				fmt.Errorf("transport do request: %w", ctx.Err()),
				nil,
			)
			// return nil
		}

		return newTransportErr(
			transportErrRetry,
			fmt.Errorf("transport do request: execute request: %w", err),
			nil,
		)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return newTransportErr(
			transportErrRetry,
			fmt.Errorf("transport do request: read response body: %w", err),
			nil,
		)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return newTransportErr(
			transportErrFatal,
			fmt.Errorf("transport do request: unmarshal response body: %w", err),
			nil,
		)
	}

	return nil
}

// lazy to check status code, don't mind exhausting retries
func (t *tgTransport) newThread(ctx context.Context, chatId tgInt, name string) (*tgInt, *transportErr) {

	payload, err := json.Marshal(&struct {
		ChatId tgInt  `json:"chat_id"`
		Name   string `json:"name"`
	}{
		ChatId: chatId,
		Name:   name,
	})
	if err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport create thread: marshal request for thread %q: %w",
				name,
				err,
			),
			nil,
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.buildUrl(tgCreateThreadEP),
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport create thread: build request for thread %q: %w",
				name,
				err,
			),
			nil,
		)
	}

	req.Header.Set("Content-Type", "application/json")

	type response struct {
		tgBaseResponse
		Result struct {
			MessageThreadId tgInt `json:"message_thread_id"`
		} `json:"result"`
	}

	var res response

	if err := t.doRequest(ctx, req, &res); err != nil {
		return nil, newTransportErr(
			err.kind,
			fmt.Errorf(
				"transport create thread: execute request for thread %q: %w",
				name,
				err.Unwrap(),
			),
			nil,
		)
	}

	if !res.Ok {
		return nil, newTransportErr(
			transportErrRetry,
			fmt.Errorf(
				"transport create thread: telegram rejected thread %q: %s",
				name,
				res.Description,
			),
			nil,
		)
	}

	threadID := res.Result.MessageThreadId
	return &threadID, nil
}

func (t *tgTransport) createThreadWithRetry(ctx context.Context, chatId tgInt, name string) (*tgInt, *transportErr) {

	var (
		threadID *tgInt
		err      *transportErr
	)

	backoff := BaseRetryBackoffTime

	for i := 0; i < MaxRetry; i++ {

		threadID, err = t.newThread(ctx, chatId, name)

		if err == nil || err.kind != transportErrRetry {
			break
		}

		sleep := min(backoff, MaxRetryTime)

		select {
		case <-ctx.Done():
			return nil, nil

		case <-time.After(sleep):
		}

		if backoff < MaxRetryTime {
			backoff = BaseRetryBackoffTime *
				time.Duration(1<<(i+1))
		}
	}

	if err != nil && err.kind == transportErrRetry {
		err = newTransportErr(
			transportErrClient,
			fmt.Errorf(
				"transport create thread with retry: exhausted retries for thread %q: %w",
				name,
				err.Unwrap(),
			),
			nil,
		)
	}

	return threadID, err
}

// TODO: handle http status codes along with res.Ok
func (t *tgTransport) send(ctx context.Context, msg tgSendRequest) (*tgSendMessageResponse, *transportErr) {

	ep := msg.endpoint()
	if len(ep) == 0 {
		return nil, newTransportErr(
			transportErrTerminate,
			fmt.Errorf(
				"transport send: misconfigured endpoint for type %v",
				reflect.TypeOf(msg),
			),
			nil,
		)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send: marshal request for endpoint %q: %w",
				ep,
				err,
			),
			nil,
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.buildUrl(ep),
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send: build request for endpoint %q: %w",
				ep,
				err,
			),
			nil,
		)
	}

	req.Header.Set("Content-Type", "application/json")

	var res tgSendMessageResponse

	if err := t.doRequest(ctx, req, &res); err != nil {
		return nil, newTransportErr(
			err.kind,
			fmt.Errorf(
				"transport send: execute request for endpoint %q: %w",
				ep,
				err.Unwrap(),
			),
			nil,
		)
	}

	if !res.Ok {
		return nil, newTransportErr(
			transportErrRetry,
			fmt.Errorf(
				"transport send: telegram rejected request for endpoint %q: %s",
				ep,
				res.Description,
			),
			nil,
		)
	}

	return &res, nil
}

func (t *tgTransport) sendMessageWithRetry(ctx context.Context, payload tgSendRequest) (*tgSendMessageResponse, *transportErr) {

	var (
		err *transportErr
		res *tgSendMessageResponse
	)

	rtime := BaseRetryBackoffTime

	for i := 0; i < MaxRetry; i++ {

		res, err = t.send(ctx, payload)

		if err == nil || err.kind != transportErrRetry {
			break
		}

		sleep := min(rtime, MaxRetryTime)
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(sleep):
		}

		if rtime < MaxRetryTime {
			rtime = BaseRetryBackoffTime * time.Duration((1 << (i + 1)))
		}
	}

	if err != nil && err.kind == transportErrRetry {
		err = newTransportErr(
			transportErrClient,
			fmt.Errorf(
				"transport send with retry: exhausted retries for endpoint %q: %w",
				payload.endpoint(),
				err.Unwrap(),
			),
			payload.sessionKey(),
		)
	}

	return res, err
}

func (t *tgTransport) sendDocument(ctx context.Context, doc *tgDocInfo, pdf *os.File) (*tgSendMessageResponse, *transportErr) {

	var body bytes.Buffer

	writer := multipart.NewWriter(&body)

	if err := writer.WriteField(
		"chat_id",
		strconv.Itoa(int(doc.ChatId)),
	); err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send document: write chat_id field: %w",
				err,
			),
			nil,
		)
	}

	if doc.ThreadId != 0 {
		if err := writer.WriteField(
			"message_thread_id",
			strconv.Itoa(int(doc.ThreadId)),
		); err != nil {
			return nil, newTransportErr(
				transportErrFatal,
				fmt.Errorf(
					"transport send document: write thread id field: %w",
					err,
				),
				nil,
			)
		}
	}

	if doc.Caption != "" {
		if err := writer.WriteField(
			"caption",
			doc.Caption,
		); err != nil {
			return nil, newTransportErr(
				transportErrFatal,
				fmt.Errorf(
					"transport send document: write caption field: %w",
					err,
				),
				nil,
			)
		}
	}

	part, err := writer.CreateFormFile(
		"document",
		filepath.Base(pdf.Name()),
	)
	if err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send document: create multipart file part for %q: %w",
				pdf.Name(),
				err,
			),
			nil,
		)
	}

	if _, err := io.Copy(part, pdf); err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send document: copy pdf %q into multipart body: %w",
				pdf.Name(),
				err,
			),
			nil,
		)
	}

	if err := writer.Close(); err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send document: finalize multipart body: %w",
				err,
			),
			nil,
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.buildUrl(tgSendDocEP),
		&body,
	)
	if err != nil {
		return nil, newTransportErr(
			transportErrFatal,
			fmt.Errorf(
				"transport send document: build request for %q: %w",
				pdf.Name(),
				err,
			),
			nil,
		)
	}

	req.Header.Set(
		"Content-Type",
		writer.FormDataContentType(),
	)

	var res tgSendMessageResponse

	if err := t.doRequest(ctx, req, &res); err != nil {
		return nil, newTransportErr(
			err.kind,
			fmt.Errorf(
				"transport send document: execute request for %q: %w",
				pdf.Name(),
				err.Unwrap(),
			),
			nil,
		)
	}

	if !res.Ok {
		return nil, newTransportErr(
			transportErrRetry,
			fmt.Errorf(
				"transport send document: telegram rejected document %q: %s",
				pdf.Name(),
				res.Description,
			),
			nil,
		)
	}

	return &res, nil
}

func (t *tgTransport) sendDocWithRetry(ctx context.Context, d *tgDocInfo, file *os.File) (*tgSendMessageResponse, *transportErr) {

	var (
		err *transportErr
		res *tgSendMessageResponse
	)

	rtime := BaseRetryBackoffTime

	for i := 0; i < MaxRetry; i++ {

		_, fErr := file.Seek(0, 0)
		if fErr != nil {
			return nil, newTransportErr(
				transportErrClient,
				fmt.Errorf(
					"transport send document with retry: rewind file %q: %w",
					file.Name(),
					fErr,
				),
				nil,
			)
		}

		res, err = t.sendDocument(ctx, d, file)

		if err == nil || err.kind != transportErrRetry {
			break
		}

		sleep := min(rtime, MaxRetryTime)
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(sleep):
		}

		if rtime < MaxRetryTime {
			rtime = BaseRetryBackoffTime * time.Duration((1 << (i + 1)))
		}
	}

	if err != nil && err.kind == transportErrRetry {
		err = newTransportErr(
			transportErrClient,
			fmt.Errorf(
				"transport send document with retry: exhausted retries for %q: %w",
				file.Name(),
				err.Unwrap(),
			),
			nil,
		)
	}

	return res, err
}

func (t *tgTransport) start(ctx context.Context) *transportErr {

	transportCtx, cancelTransportCtx := context.WithCancel(ctx)
	defer cancelTransportCtx()
	defer t.cleanup()

	go t.poll(transportCtx)
	go t.pruneSession(transportCtx)

	for {
		select {
		case msg, ok := <-t.messageCh:
			if !ok {
				return nil
			}
			go func() {
				err := t.handleMessage(transportCtx, msg)
				if err != nil {
					t.pushToChannel(transportCtx, err)
				}
			}()
		case cb, ok := <-t.callBackCh:
			if !ok {
				return nil
			}
			go func() {
				err := t.handleCallback(transportCtx, cb)
				if err != nil {
					t.pushToChannel(transportCtx, err)
				}
			}()
		case err, ok := <-t.tranportErrCh:
			if !ok {
				return nil
			}
			err = t.handleError(transportCtx, err)
			if err != nil {
				return newTransportErr(
					err.kind,
					fmt.Errorf(
						"transport start: handle transport error: %w",
						err.Unwrap(),
					),
					err.sessionKey,
				)
			}
		case <-transportCtx.Done():
			return nil
		}
	}
}

func (t *tgTransport) handleMessage(ctx context.Context, msg *tgUpdMessage) *transportErr {

	key := tgSessionKey{
		chatId:   msg.Chat.Id,
		threadId: msg.MessageThreadId,
	}

	t.sessionMapLock.Lock()
	sess, ok := t.sessionMap[key]

	if !ok {
		onThreadCreated := func(chatId, threadId tgInt) {
			t.sessionMapLock.Lock()
			defer t.sessionMapLock.Unlock()

			oldKey := tgSessionKey{
				chatId:   chatId,
				threadId: 0,
			}
			session := t.sessionMap[oldKey]
			delete(t.sessionMap, oldKey)

			newKey := tgSessionKey{
				chatId:   chatId,
				threadId: threadId,
			}

			t.sessionMap[newKey] = session
		}

		conf := &tgSessionConf{
			registry:        t.registry,
			transport:       t,
			onThreadCreated: onThreadCreated,
		}

		sess = newSession(msg.Chat.Id, conf, t.ravenConf)

		sessionKey := tgSessionKey{
			chatId:   sess.chatId,
			threadId: 0,
		}

		t.sessionMap[sessionKey] = sess
	}

	t.sessionMapLock.Unlock()

	if err := sess.msgRouter(ctx, msg); err != nil {
		return newTransportErr(
			err.kind,
			fmt.Errorf(
				"transport handle message: session %+v: %w",
				key,
				err.Unwrap(),
			),
			err.sessionKey,
		)
	}

	return nil
}

func (t *tgTransport) handleCallback(ctx context.Context, cb *tgCallBackQuery) *transportErr {

	key := tgSessionKey{
		chatId:   cb.Message.Chat.Id,
		threadId: cb.Message.MessageThreadId,
	}

	t.sessionMapLock.Lock()
	sess, ok := t.sessionMap[key]
	t.sessionMapLock.Unlock()
	if !ok {
		return newTransportErr(
			transportErrClient,
			fmt.Errorf(
				"transport handle callback: session not found for chat %d thread %d",
				key.chatId,
				key.threadId,
			),
			&key,
		)
	}

	if err := sess.cbRouter(ctx, cb); err != nil {
		return newTransportErr(
			err.kind,
			fmt.Errorf(
				"transport handle callback: session %+v: %w",
				key,
				err.Unwrap(),
			),
			err.sessionKey,
		)
	}

	return nil
}

func (t *tgTransport) handleError(ctx context.Context, err *transportErr) *transportErr {

	switch err.kind {
	case transportErrClient:
		errMsg := &tgNewMessage{
			reqEndpoint: tgSendNewMessageEP,
			ChatId:      err.sessionKey.chatId,
			ThreadId:    err.sessionKey.threadId,
			Text:        "client error : " + err.Unwrap().Error(),
		}
		go func() {
			_, _ = t.sendMessageWithRetry(ctx, errMsg)
		}()
		// TODO: maybe audit log here
		t.sessionMapLock.Lock()
		delete(t.sessionMap, *err.sessionKey)
		t.sessionMapLock.Unlock()
	default:
		return newTransportErr(
			err.kind,
			fmt.Errorf(
				"transport handle error: severe error : %w",
				err.Unwrap(),
			),
			err.sessionKey,
		)
	}
	return nil
}

func (t *tgTransport) pruneSession(ctx context.Context) {

	for {

		var expired []tgSessionKey
		t.sessionMapLock.Lock()
		for key, sess := range t.sessionMap {
			sess.lastActiveLock.Lock()
			if time.Since(sess.lastActiveAt) > sessionExpiry {

				sess.dataLock.Lock()
				if sess.data != nil && sess.data.diagnoseMachine != nil {
					sess.data.diagnoseMachine.cancelDiagnosis()
				}
				sess.dataLock.Unlock()

				expired = append(expired, key)
				delete(t.sessionMap, key)
			}
			sess.lastActiveLock.Unlock()
		}
		t.sessionMapLock.Unlock()

		for _, e := range expired {
			expireMsg := &tgNewMessage{
				reqEndpoint: tgSendNewMessageEP,
				ChatId:      e.chatId,
				ThreadId:    e.threadId,
				Text:        "session expired",
			}
			_, err := t.send(ctx, expireMsg)
			if err != nil {
				t.pushToChannel(
					ctx,
					newTransportErr(
						err.kind,
						fmt.Errorf(
							"transport prune session: notify expired session %+v: %w",
							e,
							err.Unwrap(),
						),
						err.sessionKey,
					),
				)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(pruneInterval):
		}
	}
}

func (t *tgTransport) cleanup() {

	t.closeOnce.Do(func() {

		t.sessionMapLock.Lock()

		sessions := make([]*tgSession, 0, len(t.sessionMap))
		for _, sess := range t.sessionMap {
			sessions = append(sessions, sess)
		}

		clear(t.sessionMap)

		t.sessionMapLock.Unlock()

		for _, sess := range sessions {

			sess.dataLock.Lock()
			if sess.data != nil &&
				sess.data.diagnoseMachine != nil &&
				sess.data.diagnoseMachine.agent != nil {

				_ = sess.data.diagnoseMachine.agent.cleanUp()
			}
			sess.dataLock.Unlock()
		}
	})
}

type transportErr struct {
	kind       transportErrKind
	err        error
	sessionKey *tgSessionKey
}

func newTransportErr(kind transportErrKind, err error, sessionKey *tgSessionKey) *transportErr {
	tErr := &transportErr{
		kind: kind,
		err:  err,
	}

	if sessionKey != nil {
		tErr.sessionKey = sessionKey
	}

	return tErr
}

func (t *transportErr) Unwrap() error {
	return t.err
}

func (t *transportErr) Error() string {
	return t.err.Error()
}
