package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type transportConf struct {
	addr     string
	botToken string
	ctx      context.Context
}

type transport struct {
	client *http.Client
	offset tgId
	*transportConf
	requestCh chan *request
}

func newTransport(conf *transportConf) *transport {
	return &transport{
		client: &http.Client{
			Timeout: clientTimeout,
		},
		offset:        0,
		transportConf: conf,
		requestCh:     make(chan *request, 10),
	}
}

func (t *transport) poll() error {

	defer close(t.requestCh)
	
	for {

		params := url.Values{}
		params.Set("offset", strconv.Itoa(int(t.offset)))
		params.Set("timeout", strconv.Itoa(pollTimeout))
		params.Set("limit", strconv.Itoa(getMethodLimit))
		params.Set("allowed_updates", `["message"]`)

		reqUrl := t.addr + t.botToken + "/" + "getUpdates" + "?" + params.Encode()
		req, err := http.NewRequestWithContext(t.ctx, http.MethodGet, reqUrl, nil)
		if err != nil {
			return err
		}

		res, err := t.client.Do(req)
		if err != nil {

			if t.ctx.Err() != nil {
				return t.ctx.Err()
			}

			log.Printf("error : %s\n", err.Error())
			time.Sleep(pollRetryBackoff)
			continue
		}

		body, err := io.ReadAll(res.Body)
		if err != nil {
			log.Printf("error : %s\n", err.Error())
			res.Body.Close()
			continue
		}
		res.Body.Close()

		var resp tgGetUpdateResponse
		err = json.Unmarshal(body, &resp)
		if err != nil {
			log.Printf("error : %s\n", err.Error())
			continue
		}

		if !resp.Ok {
			log.Printf("error code : %d \n message : %s\n", resp.ErrorCode, resp.Description)
			time.Sleep(pollRetryBackoff)
			continue
		}

		if len(resp.Result) > 0 {

			t.offset = resp.Result[len(resp.Result)-1].UpdateId + 1

			for _, upd := range resp.Result {
				userReq := &request{
					messageId: upd.Message.MessageId,
					updateId:  upd.UpdateId,
					chatId:    upd.Message.Chat.Id,
					userId:    upd.Message.From.Id,
					query:     upd.Message.Message,
				}

				select {
				case t.requestCh <- userReq:
				case <-t.ctx.Done():
					return t.ctx.Err()
				}
			}
		}
	}
}

func (t *transport) send(chatId tgId, text string) error {

	payload, err := json.Marshal(&struct {
		ChatId tgId   `json:"chat_id"`
		Text   string `json:"text"`
	}{
		ChatId: chatId,
		Text:   text,
	})

	if err != nil {
		return err
	}

	body := bytes.NewReader(payload)

	reqURL := t.addr + t.botToken + "/" + "sendMessage"
	req, err := http.NewRequestWithContext(t.ctx, "POST", reqURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var response tgSendMessageResponse
	err = json.Unmarshal(respBody, &response)
	if err != nil {
		return err
	}
	
	if !response.Ok {
		return fmt.Errorf("%s", response.Description)
	}

	return nil
}

func (t *transport) requests() <- chan *request {
	return t.requestCh
}

