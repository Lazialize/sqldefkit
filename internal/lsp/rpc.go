// Package lsp implements a minimal Language Server Protocol server for
// sqldefkit, providing exactly two features: diagnostics (from the check
// engine) and go-to-definition. It hand-rolls JSON-RPC 2.0 framing over
// stdio (Content-Length headers) rather than depending on a third-party
// LSP library, since only a handful of message types are needed.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// jsonrpcVersion is the fixed "jsonrpc" field value on every message.
const jsonrpcVersion = "2.0"

// requestMessage is the wire shape of an incoming request or notification.
// Requests carry a non-nil ID; notifications omit it.
type requestMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// responseMessage is the wire shape of an outgoing response to a request.
type responseMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

// notificationMessage is the wire shape of an outgoing notification (no ID).
type notificationMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// responseError is a JSON-RPC error object.
type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC / LSP error codes actually used here.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// reader reads a stream of LSP base-protocol-framed JSON-RPC messages from
// an underlying io.Reader.
type reader struct {
	br *bufio.Reader
}

func newReader(r io.Reader) *reader {
	return &reader{br: bufio.NewReader(r)}
}

// readMessage reads one framed message (headers + body) and returns the
// raw body bytes. It returns io.EOF (possibly wrapped) when the stream
// ends cleanly between messages.
func (r *reader) readMessage() ([]byte, error) {
	var contentLength int
	haveLength := false

	for {
		line, err := r.br.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("reading header line: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// Blank line: end of headers.
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if strings.EqualFold(name, "Content-Length") {
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length %q: %w", value, err)
			}
			contentLength = n
			haveLength = true
		}
	}

	if !haveLength {
		return nil, fmt.Errorf("message missing Content-Length header")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r.br, body); err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}
	return body, nil
}

// writer writes LSP base-protocol-framed JSON-RPC messages to an
// underlying io.Writer. All writes are serialized through mu so
// concurrent goroutines never interleave output.
type writer struct {
	mu sync.Mutex
	w  io.Writer
}

func newWriter(w io.Writer) *writer {
	return &writer{w: w}
}

func (w *writer) writeMessage(body []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := fmt.Fprintf(w.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.w.Write(body)
	return err
}

func (w *writer) writeResponse(id json.RawMessage, result any, respErr *responseError) error {
	msg := responseMessage{JSONRPC: jsonrpcVersion, ID: id, Error: respErr}
	if respErr == nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		msg.Result = raw
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return w.writeMessage(body)
}

func (w *writer) writeNotification(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	msg := notificationMessage{JSONRPC: jsonrpcVersion, Method: method, Params: raw}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return w.writeMessage(body)
}
