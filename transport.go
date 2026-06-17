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
}

type tgTransport struct {
	*tgTransportConf

	client *http.Client
	offset tgInt

	sessionMapLock *sync.Mutex
	sessionMap     map[tgSessionKey]*tgSession

	messageCh     chan *tgMessage
	callBackCh    chan *tgCallBackQuery
	tranportErrCh chan *transportErr

	ravenConf *ravenConfig
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

		messageCh:     make(chan *tgMessage, 20),
		callBackCh:    make(chan *tgCallBackQuery, 20),
		tranportErrCh: make(chan *transportErr, 20),

		ravenConf: ravenConf,
	}

	return t
}

func (t *tgTransport) authenticate(userId tgInt) error {
	if userId != t.userId {
		return fmt.Errorf("authentication failed")
	}

	return nil
}

func (t *tgTransport) pushTransportErr(kind transportErrKind, err error, chatId ...tgInt) {

	pollErr := &transportErr{
		kind: kind,
		err:  err,
	}

	if len(chatId) > 0 {
		pollErr.chatId = chatId[0]
	}

	t.tranportErrCh <- pollErr
}

// TODO:Better name this method
func (t *tgTransport) makeUrl(ep tgEndpoint) string {
	return string(tgAPIUrl) + t.botToken + "/" + string(ep)
}

func (t *tgTransport) poll(ctx context.Context) {

	defer close(t.messageCh)
	defer close(t.callBackCh)
	defer close(t.tranportErrCh)

	for {

		params := url.Values{}
		params.Set("offset", strconv.Itoa(int(t.offset)))
		params.Set("timeout", strconv.Itoa(pollTimeout))
		params.Set("limit", strconv.Itoa(getMethodLimit))
		params.Set("allowed_updates", `["message", "callback_query"]`)

		reqUrl := t.makeUrl(tgEPGetUpdate) + "?" + params.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqUrl, nil)
		if err != nil {
			err = fmt.Errorf("%s", err.Error())
			t.pushTransportErr(transportErrFatal, err)
			return
		}

		res, err := t.client.Do(req)
		if err != nil {

			if ctx.Err() != nil {
				err = fmt.Errorf("%s", ctx.Err().Error())
				t.pushTransportErr(transportErrFatal, err)
				return
			} else {
				err = fmt.Errorf("poll error : %s", err.Error())
				t.pushTransportErr(transportErrLog, err)
				time.Sleep(pollRetryBackoff)
				continue
			}
		}

		body, err := io.ReadAll(res.Body)
		if err != nil {
			err = fmt.Errorf("poll error : %s", err.Error())
			t.pushTransportErr(transportErrLog, err)
			res.Body.Close()
			continue
		}

		var resp tgGetUpdateResponse
		err = json.Unmarshal(body, &resp)
		if err != nil {
			err = fmt.Errorf("poll error : %s", err.Error())
			t.pushTransportErr(transportErrLog, err)
			res.Body.Close()
			continue
		}
		res.Body.Close()

		if !resp.Ok {
			err = fmt.Errorf("poll error, response code : %d \n message : %s\n", resp.ErrorCode, resp.Description)
			t.pushTransportErr(transportErrLog, err)
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

					select {

					case t.messageCh <- msg:

					case <-ctx.Done():
						err = fmt.Errorf("%s", ctx.Err().Error())
						t.pushTransportErr(transportErrFatal, err)
						return
					}

				case upd.CallBackQuery != nil:

					cb := upd.CallBackQuery

					if err := t.authenticate(cb.From.Id); err != nil {
						continue
					}

					select {

					case t.callBackCh <- cb:

					case <-ctx.Done():
						err = fmt.Errorf("%s", ctx.Err().Error())
						t.pushTransportErr(transportErrFatal, err)
						return
					}
				}
			}
		}
	}
}

func (t *tgTransport) newThread(ctx context.Context, chatId tgInt, name string) (*tgInt, error) {

	payload, err := json.Marshal(&struct {
		ChatId tgInt  `json:"chat_id"`
		Name   string `json:"name"`
	}{
		ChatId: chatId,
		Name:   name,
	})

	if err != nil {
		return nil, err
	}

	body := bytes.NewReader(payload)

	reqUrl := t.makeUrl(tgEPCreateThread)
	req, err := http.NewRequestWithContext(ctx, "POST", reqUrl, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	httpRes, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpRes.Body.Close()

	data, err := io.ReadAll(httpRes.Body)
	if err != nil {
		return nil, err
	}

	type response struct {
		*tgBaseResponse
		Result struct {
			MessageThreadId tgInt `json:"message_thread_id"`
		} `json:"result"`
	}

	var res response

	err = json.Unmarshal(data, &res)
	if err != nil {
		return nil, err
	}

	if !res.Ok {
		return nil, fmt.Errorf("transport create thread error : %s", res.Description)
	}

	threadId := res.Result.MessageThreadId

	return &threadId, nil
}

func (t *tgTransport) send(ctx context.Context, msg any, ep tgEndpoint) (*tgSendMessageResponse, error) {

	// TODO:maybe add a switch case guard on msg type to enforce right type on right endpoint
	payload, err := json.Marshal(msg)

	if err != nil {
		return nil, err
	}

	body := bytes.NewReader(payload)

	reqURL := t.makeUrl(ep)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response tgSendMessageResponse
	err = json.Unmarshal(respBody, &response)
	if err != nil {
		return nil, err
	}

	if !response.Ok {
		return nil, fmt.Errorf("%s", response.Description)
	}

	return &response, nil
}

func (t *tgTransport) sendDocument(ctx context.Context, doc *tgSendDoc, pdf *os.File) error {

	var body bytes.Buffer

	writer := multipart.NewWriter(&body)

	writer.WriteField("chat_id", strconv.Itoa(int(doc.ChatId)))

	if doc.ThreadId != 0 {
		writer.WriteField(
			"message_thread_id",
			strconv.Itoa(int(doc.ThreadId)),
		)
	}

	if len(doc.Caption) != 0 {
		writer.WriteField("caption", doc.Caption)
	}

	part, err := writer.CreateFormFile("document", filepath.Base(pdf.Name()))
	if err != nil {
		return err
	}

	_, err = io.Copy(part, pdf)
	if err != nil {
		return err
	}

	err = writer.Close()
	if err != nil {
		return err
	}

	reqUrl := t.makeUrl(tgEPSendDoc)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqUrl, &body)
	if err != nil {
		return err
	}
	req.Header.Set(
		"Content-Type",
		writer.FormDataContentType(),
	)

	res, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		} else {
			return err
		}
	}
	defer res.Body.Close()

	resbody, err := io.ReadAll(res.Body)
	if err != nil {
		err = fmt.Errorf("poll error : %s", err.Error())
		return err
	}

	//TODO:Generic response type ?
	var sendRes tgSendMessageResponse
	err = json.Unmarshal(resbody, &sendRes)
	if err != nil {
		return err
	}

	if !sendRes.Ok {
		return fmt.Errorf("%s", sendRes.Description)
	}

	return nil
}

func (t *tgTransport) start(ctx context.Context) {

	go t.poll(ctx)
	go t.pruneSession(ctx)

	for {

		select {
		case msg, ok := <-t.messageCh:
			if !ok {
				break
			}
			go t.handleMessage(ctx, msg)
		case cb, ok := <-t.callBackCh:
			if !ok {
				break
			}
			go t.handleCallback(ctx, cb)
		case err, ok := <-t.tranportErrCh:
			if !ok {
				break
			}
			go t.handleTransportError(err)
		case <-ctx.Done():
		}
	}
}

func (t *tgTransport) handleMessage(ctx context.Context, msg *tgMessage) error {

	t.sessionMapLock.Lock()
	defer t.sessionMapLock.Unlock()

	if oldSess, ok := t.sessionMap[tgSessionKey{chatId: msg.Chat.Id, threadId: msg.MessageThreadId}]; ok {
		return oldSess.msgRouter(ctx, msg)
	}

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
		transport:       t,
		onThreadCreated: onThreadCreated,
	}

	session := newSession(msg.Chat.Id, conf, t.ravenConf)

	sessionKey := tgSessionKey{
		chatId:   session.chatId,
		threadId: 0,
	}
	t.sessionMap[sessionKey] = session

	return session.msgRouter(ctx, msg)
}

func (t *tgTransport) handleCallback(ctx context.Context, cb *tgCallBackQuery) error {

	key := tgSessionKey{
		chatId:   cb.Message.Chat.Id,
		threadId: cb.Message.MessageThreadId,
	}

	t.sessionMapLock.Lock()
	sess, ok := t.sessionMap[key]
	t.sessionMapLock.Unlock()
	if !ok {
		return fmt.Errorf("session for callback not found") // write this error again
	}
	return sess.cbRouter(ctx, cb)
}

func (t *tgTransport) handleTransportError(err *transportErr) {

}

func (t *tgTransport) pruneSession(ctx context.Context) {

}

func (t *tgTransport) close() {}
