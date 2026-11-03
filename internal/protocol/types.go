package protocol

import "encoding/json"

// ProtocolVersion is the current protocol version.
const ProtocolVersion = 1

// request is a JSON-RPC 2.0 request sent to an actor.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is a JSON-RPC 2.0 response from an actor.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// responseError is the error object in a JSON-RPC 2.0 response.
type responseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// LogNotification holds the params of a scraps/log notification.
type LogNotification struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// InitializeParams are the params for scraps/initialize.
type InitializeParams struct {
	Protocol  int               `json:"protocol"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Inputs    map[string]any    `json:"inputs"`
	Subjects  map[string]string `json:"subjects"`
	Seed      int64             `json:"seed"`
	Workdir   string            `json:"workdir"`
	Artefacts string            `json:"artefacts"`
}

// InitializeResult is the result of scraps/initialize.
type InitializeResult struct {
	Protocol int            `json:"protocol"`
	Outputs  map[string]any `json:"outputs"`
}

// LogHandler is a callback for scraps/log notifications.
type LogHandler func(level, message string)
