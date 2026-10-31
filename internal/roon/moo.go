package roon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// MooMessage represents a parsed MOO/1 frame.
type MooMessage struct {
	Verb          string
	Service       string
	Name          string
	RequestID     string
	ContentType   string
	ContentLength int
	Headers       map[string]string
	Body          []byte
}

// MooCallback handles responses (CONTINUE / COMPLETE) for a sent REQUEST.
type MooCallback func(msg *MooMessage, body []byte)

// MooRequestHandler handles incoming REQUEST frames sent by the Roon Core to our extension.
type MooRequestHandler func(conn *MooConn, msg *MooMessage)

// MooConn manages a single WebSocket MOO/1 connection to a Roon Core.
type MooConn struct {
	Host      string
	Port      string
	UniqueID  string
	ws        *websocket.Conn
	writeMu   sync.Mutex
	reqMu     sync.Mutex
	nextReqID int64
	nextSubID int64
	requests  map[string]MooCallback
	onRequest MooRequestHandler
	onClose   func()
	closeOnce sync.Once
	closedCh  chan struct{}
}

// DialMoo connects to ws://<host>:<port>/api and starts the MOO read/heartbeat loops.
func DialMoo(host, port, uniqueID string, onRequest MooRequestHandler, onClose func()) (*MooConn, error) {
	wsURL := fmt.Sprintf("ws://%s:%s/api", host, port)
	dialer := websocket.Dialer{
		HandshakeTimeout: 8 * time.Second,
	}
	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}

	mc := &MooConn{
		Host:      host,
		Port:      port,
		UniqueID:  uniqueID,
		ws:        ws,
		requests:  make(map[string]MooCallback),
		onRequest: onRequest,
		onClose:   onClose,
		closedCh:  make(chan struct{}),
	}

	ws.SetPongHandler(func(appData string) error {
		return nil
	})

	go mc.readLoop()
	go mc.pingLoop()

	return mc, nil
}

func (mc *MooConn) Close() {
	mc.closeOnce.Do(func() {
		close(mc.closedCh)
		mc.writeMu.Lock()
		_ = mc.ws.Close()
		mc.writeMu.Unlock()

		mc.reqMu.Lock()
		cbs := make([]MooCallback, 0, len(mc.requests))
		for k, cb := range mc.requests {
			if cb != nil {
				cbs = append(cbs, cb)
			}
			delete(mc.requests, k)
		}
		mc.reqMu.Unlock()

		for _, cb := range cbs {
			cb(nil, nil)
		}

		if mc.onClose != nil {
			mc.onClose()
		}
	})
}

func (mc *MooConn) pingLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-mc.closedCh:
			return
		case <-ticker.C:
			mc.writeMu.Lock()
			err := mc.ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			mc.writeMu.Unlock()
			if err != nil {
				mc.Close()
				return
			}
		}
	}
}

func (mc *MooConn) readLoop() {
	defer mc.Close()
	for {
		_, data, err := mc.ws.ReadMessage()
		if err != nil {
			return
		}
		msg, err := parseMooFrame(data)
		if err != nil {
			log.Printf("[MOO] Frame parse error: %v", err)
			continue
		}

		if msg.Verb == "REQUEST" {
			if mc.onRequest != nil {
				go mc.onRequest(mc, msg)
			} else {
				_ = mc.SendComplete(msg.RequestID, "InvalidRequest", map[string]string{
					"error": "unknown service: " + msg.Service,
				})
			}
		} else {
			mc.reqMu.Lock()
			cb, ok := mc.requests[msg.RequestID]
			if ok && msg.Verb == "COMPLETE" {
				delete(mc.requests, msg.RequestID)
			}
			mc.reqMu.Unlock()

			if ok && cb != nil {
				cb(msg, msg.Body)
			}
		}
	}
}

// SendRequest sends an asynchronous MOO/1 REQUEST frame and registers a callback for CONTINUE/COMPLETE responses.
func (mc *MooConn) SendRequest(name string, body any, cb MooCallback) (string, error) {
	reqID := strconv.FormatInt(atomic.AddInt64(&mc.nextReqID, 1)-1, 10)

	var bodyBytes []byte
	var contentType string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		bodyBytes = b
		contentType = "application/json"
	}

	frame := buildMooFrame("REQUEST", name, reqID, contentType, bodyBytes)

	if cb != nil {
		mc.reqMu.Lock()
		mc.requests[reqID] = cb
		mc.reqMu.Unlock()
	}

	mc.writeMu.Lock()
	err := mc.ws.WriteMessage(websocket.BinaryMessage, frame)
	mc.writeMu.Unlock()
	if err != nil {
		mc.reqMu.Lock()
		delete(mc.requests, reqID)
		mc.reqMu.Unlock()
		return "", err
	}
	return reqID, nil
}

// Call performs a single request/response call and waits for the first response or context timeout.
func (mc *MooConn) Call(ctx context.Context, name string, reqBody any, respDest any) (*MooMessage, error) {
	type res struct {
		msg  *MooMessage
		body []byte
	}
	ch := make(chan res, 1)
	_, err := mc.SendRequest(name, reqBody, func(msg *MooMessage, body []byte) {
		select {
		case ch <- res{msg: msg, body: body}:
		default:
		}
	})
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.msg == nil {
			return nil, errors.New("connection closed")
		}
		if respDest != nil && len(r.body) > 0 && r.msg.ContentType == "application/json" {
			if err := json.Unmarshal(r.body, respDest); err != nil {
				return r.msg, err
			}
		}
		return r.msg, nil
	}
}

// Subscribe initiates a MOO subscription using a unique subscription_key.
func (mc *MooConn) Subscribe(svcName, subName string, args map[string]any, cb MooCallback) (func(), error) {
	subKey := atomic.AddInt64(&mc.nextSubID, 1) - 1
	reqArgs := make(map[string]any)
	for k, v := range args {
		reqArgs[k] = v
	}
	reqArgs["subscription_key"] = subKey

	_, err := mc.SendRequest(svcName+"/subscribe_"+subName, reqArgs, cb)
	if err != nil {
		return nil, err
	}

	unsub := func() {
		_, _ = mc.SendRequest(svcName+"/unsubscribe_"+subName, map[string]any{
			"subscription_key": subKey,
		}, nil)
	}
	return unsub, nil
}

// SendContinue sends a MOO/1 CONTINUE response for an incoming REQUEST.
func (mc *MooConn) SendContinue(reqID, name string, body any) error {
	return mc.sendReply("CONTINUE", reqID, name, body)
}

// SendComplete sends a MOO/1 COMPLETE response for an incoming REQUEST.
func (mc *MooConn) SendComplete(reqID, name string, body any) error {
	return mc.sendReply("COMPLETE", reqID, name, body)
}

func (mc *MooConn) sendReply(verb, reqID, name string, body any) error {
	var bodyBytes []byte
	var contentType string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyBytes = b
		contentType = "application/json"
	}
	frame := buildMooFrame(verb, name, reqID, contentType, bodyBytes)
	mc.writeMu.Lock()
	defer mc.writeMu.Unlock()
	return mc.ws.WriteMessage(websocket.BinaryMessage, frame)
}

func buildMooFrame(verb, name, reqID, contentType string, body []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("MOO/1 %s %s\n", verb, name))
	buf.WriteString(fmt.Sprintf("Request-Id: %s\n", reqID))
	if len(body) > 0 {
		buf.WriteString(fmt.Sprintf("Content-Length: %d\n", len(body)))
		buf.WriteString(fmt.Sprintf("Content-Type: %s\n", contentType))
	}
	buf.WriteByte('\n')
	if len(body) > 0 {
		buf.Write(body)
	}
	return buf.Bytes()
}

func parseMooFrame(data []byte) (*MooMessage, error) {
	if len(data) == 0 {
		return nil, errors.New("empty MOO frame")
	}
	msg := &MooMessage{
		Headers: make(map[string]string),
	}

	// Find blank line (\n\n or \r\n\r\n)
	pos := 0
	firstLine := true
	for pos < len(data) {
		nlIdx := bytes.IndexByte(data[pos:], '\n')
		if nlIdx < 0 {
			return nil, errors.New("MOO frame missing header newline")
		}
		lineBytes := data[pos : pos+nlIdx]
		if len(lineBytes) > 0 && lineBytes[len(lineBytes)-1] == '\r' {
			lineBytes = lineBytes[:len(lineBytes)-1]
		}
		line := string(lineBytes)
		pos += nlIdx + 1

		if firstLine {
			firstLine = false
			// Format: MOO/1 <VERB> <NAME>
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 3 || !strings.HasPrefix(parts[0], "MOO/") {
				return nil, fmt.Errorf("bad MOO first line: %q", line)
			}
			msg.Verb = parts[1]
			rest := parts[2]
			if msg.Verb == "REQUEST" {
				slashIdx := strings.IndexByte(rest, '/')
				if slashIdx < 0 {
					return nil, fmt.Errorf("bad MOO REQUEST target: %q", rest)
				}
				msg.Service = rest[:slashIdx]
				msg.Name = rest[slashIdx+1:]
			} else {
				msg.Name = rest
			}
			continue
		}

		// Blank line marks end of headers
		if len(line) == 0 {
			if msg.RequestID == "" {
				return nil, errors.New("MOO missing Request-Id")
			}
			if msg.ContentLength > 0 {
				end := pos + msg.ContentLength
				if end > len(data) {
					end = len(data)
				}
				msg.Body = data[pos:end]
			}
			return msg, nil
		}

		colonIdx := strings.IndexByte(line, ':')
		if colonIdx < 0 {
			return nil, fmt.Errorf("bad MOO header line: %q", line)
		}
		k := strings.TrimSpace(line[:colonIdx])
		v := strings.TrimSpace(line[colonIdx+1:])
		switch k {
		case "Content-Type":
			msg.ContentType = v
		case "Content-Length":
			cl, _ := strconv.Atoi(v)
			msg.ContentLength = cl
		case "Request-Id":
			msg.RequestID = v
		default:
			msg.Headers[k] = v
		}
	}

	return nil, errors.New("unexpected end of MOO headers")
}
