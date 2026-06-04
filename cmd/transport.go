package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type transportConf struct {
	userId   tgInt
	addr     string
	botToken string
	ctx      context.Context
}

type pollErr struct {
	kind   pollErrKind
	err    error
	chatId tgInt
}

type transport struct {
	*transportConf

	client      *http.Client
	offset      tgInt
	commandsMap map[string]struct{}
	messageCh   chan *tgMessage
	callBackCh  chan *tgCallBackQuery
	pollErr     chan *pollErr
}

func newTransport(conf *transportConf) *transport {

	cmdMap := map[string]struct{}{
		cmdDiagnose: {},
	}
	t := &transport{
		client: &http.Client{
			Timeout: clientTimeout,
		},
		offset:        0,
		commandsMap:   cmdMap,
		transportConf: conf,
		messageCh:     make(chan *tgMessage, 10),
		callBackCh:    make(chan *tgCallBackQuery, 10),
		pollErr:       make(chan *pollErr, 10),
	}

	return t
}

func (t *transport) authenticate(userId tgInt) error {
	if userId != t.userId {
		return fmt.Errorf("authentication failed")
	}

	return nil
}

func (t *transport) pushPollErr(kind pollErrKind, err error, chatId ...tgInt) {

	pollErr := &pollErr{
		kind: kind,
		err:  err,
	}

	if len(chatId) > 0 {
		pollErr.chatId = chatId[0]
	}

	t.pollErr <- pollErr
}

func (t *transport) poll() {

	defer close(t.messageCh)
	defer close(t.callBackCh)
	defer close(t.pollErr)

	for {

		params := url.Values{}
		params.Set("offset", strconv.Itoa(int(t.offset)))
		params.Set("timeout", strconv.Itoa(pollTimeout))
		params.Set("limit", strconv.Itoa(getMethodLimit))
		params.Set("allowed_updates", `["message", "callback_query"]`)

		reqUrl := t.addr + t.botToken + "/" + endpointGetUpdate + "?" + params.Encode()
		req, err := http.NewRequestWithContext(t.ctx, http.MethodGet, reqUrl, nil)
		if err != nil {
			t.pushPollErr(pollFatalErr, err)
			return
		}

		res, err := t.client.Do(req)
		if err != nil {

			if t.ctx.Err() != nil {
				t.pushPollErr(pollFatalErr, t.ctx.Err())
				return
			} else {
				t.pushPollErr(pollLogErr, fmt.Errorf("poll error : %s\n", err.Error()))
				time.Sleep(pollRetryBackoff)
				continue
			}
		}

		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.pushPollErr(pollLogErr, fmt.Errorf("poll error : %s\n", err.Error()))
			res.Body.Close()
			continue
		}

		var resp tgGetUpdateResponse
		err = json.Unmarshal(body, &resp)
		if err != nil {
			t.pushPollErr(pollLogErr, fmt.Errorf("poll error : %s\n", err.Error()))
			res.Body.Close()
			continue
		}
		res.Body.Close()

		if !resp.Ok {
			err = fmt.Errorf("poll error,  code : %d \n message : %s\n", resp.ErrorCode, resp.Description)
			t.pushPollErr(pollLogErr, err)
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

					if len(msg.Entities) != 1 {
						err = fmt.Errorf("please use ONE of supported raven commands")
						t.pushPollErr(pollClientErr, err, msg.From.Id)
						continue
					}

					entity := msg.Entities[0]
					if entity.Type != cmd {
						err = fmt.Errorf("please use raven commands")
						t.pushPollErr(pollClientErr, err, msg.From.Id)
						continue
					}

					if entity.Offset != 0 {
						err = fmt.Errorf("please use command format : /command <arg>")
						t.pushPollErr(pollClientErr, err, msg.From.Id)
						continue
					}

					if _, ok := t.commandsMap[msg.Text[0:entity.Length]]; !ok {
						err = fmt.Errorf("command : %s not supported, please use a valid command", msg.Text[0:entity.Length])
						t.pushPollErr(pollClientErr, err, msg.From.Id)
						continue
					}

					select {
					case t.messageCh <- msg:
					case <-t.ctx.Done():
						t.pushPollErr(pollFatalErr, t.ctx.Err())
						return
					}
				case upd.CallBackQuery != nil:

					cb := upd.CallBackQuery

					if err := t.authenticate(cb.From.Id); err != nil {
						continue
					}

					select {
					case t.callBackCh <- cb:
					case <-t.ctx.Done():
						t.pushPollErr(pollFatalErr, t.ctx.Err())
						return
					}
				}
			}
		}
	}
}

func (t *transport) newThread(chatId tgInt, name string) (*tgInt, error) {

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

	reqUrl := t.addr + t.botToken + "/" + endpointCreateThread
	req, err := http.NewRequestWithContext(t.ctx, "POST", reqUrl, body)
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
		*tgCommonResponse
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

func (t *transport) send(msg any, endPoint string) (*tgSendMessageResponse, error) {
	
	payload, err := json.Marshal(msg)
	
	if err != nil {
		return nil, err
	}

	body := bytes.NewReader(payload)

	reqURL := t.addr + t.botToken + "/" + endPoint
	req, err := http.NewRequestWithContext(t.ctx, "POST", reqURL, body)
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

func (t *transport) messages() <-chan *tgMessage {
	return t.messageCh
}

func (t *transport) callBacks() <-chan *tgCallBackQuery {
	return t.callBackCh
}

func (t *transport) errors() <- chan *pollErr {
	return t.pollErr
}
