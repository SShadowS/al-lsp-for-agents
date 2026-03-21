package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Message represents a JSON-RPC message (request, response, or notification)
type Message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error.
// Implements custom UnmarshalJSON to handle both standard JSON-RPC error objects
// ({"code": -32600, "message": "..."}) and plain string errors ("some error")
// which the AL Language Server may return in multi-root workspace scenarios.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) UnmarshalJSON(data []byte) error {
	// Try standard object format first
	type rpcErrorAlias RPCError
	var obj rpcErrorAlias
	if err := json.Unmarshal(data, &obj); err == nil {
		*e = RPCError(obj)
		return nil
	}

	// Fall back to plain string format: "error": "some message"
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.Code = InternalError
		e.Message = s
		return nil
	}

	return fmt.Errorf("cannot unmarshal error: expected object or string, got %s", string(data))
}

// IsRequest returns true if this is a request (has method and id)
func (m *Message) IsRequest() bool {
	return m.Method != "" && m.ID != nil
}

// IsNotification returns true if this is a notification (has method but no id)
func (m *Message) IsNotification() bool {
	return m.Method != "" && m.ID == nil
}

// IsResponse returns true if this is a response (has id but no method)
func (m *Message) IsResponse() bool {
	return m.ID != nil && m.Method == ""
}

// GetIDInt returns the ID as an integer, or 0 if not an integer
func (m *Message) GetIDInt() int {
	if m.ID == nil {
		return 0
	}
	var id int
	if err := json.Unmarshal(*m.ID, &id); err != nil {
		return 0
	}
	return id
}

// GetIDString returns the ID as a string representation
func (m *Message) GetIDString() string {
	if m.ID == nil {
		return ""
	}
	return string(*m.ID)
}

// NewRequest creates a new JSON-RPC request
func NewRequest(id int, method string, params interface{}) (*Message, error) {
	idJSON, _ := json.Marshal(id)
	rawID := json.RawMessage(idJSON)

	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      &rawID,
		Method:  method,
		Params:  rawParams,
	}, nil
}

// NewNotification creates a new JSON-RPC notification
func NewNotification(method string, params interface{}) (*Message, error) {
	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}

	return &Message{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	}, nil
}

// NewResponse creates a new JSON-RPC response
func NewResponse(id *json.RawMessage, result interface{}) (*Message, error) {
	var rawResult json.RawMessage
	if result != nil {
		var err error
		rawResult, err = json.Marshal(result)
		if err != nil {
			return nil, err
		}
	} else {
		rawResult = json.RawMessage("null")
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      id,
		Result:  rawResult,
	}, nil
}

// NewErrorResponse creates a new JSON-RPC error response
func NewErrorResponse(id *json.RawMessage, code int, message string) *Message {
	return &Message{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
}

// ReadMessage reads a single LSP message from the reader
func ReadMessage(reader *bufio.Reader) (*Message, error) {
	// Read headers until empty line
	var contentLength int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)

		if line == "" {
			// End of headers
			break
		}

		if strings.HasPrefix(line, "Content-Length:") {
			lengthStr := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			contentLength, err = strconv.Atoi(lengthStr)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %s", lengthStr)
			}
		}
		// Ignore other headers like Content-Type
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	// Read the content
	content := make([]byte, contentLength)
	_, err := io.ReadFull(reader, content)
	if err != nil {
		return nil, err
	}

	// Parse JSON
	var msg Message
	if err := json.Unmarshal(content, &msg); err != nil {
		return nil, fmt.Errorf("failed to parse JSON-RPC message: %w", err)
	}

	return &msg, nil
}

// WriteMessage writes a single LSP message to the writer
func WriteMessage(writer io.Writer, msg *Message) error {
	content, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(content))
	if _, err := writer.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}

	return nil
}

// WriteRawMessage writes raw JSON bytes as an LSP message
func WriteRawMessage(writer io.Writer, content []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(content))
	if _, err := writer.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	return nil
}

// LSP Error Codes
const (
	ParseError           = -32700
	InvalidRequest       = -32600
	MethodNotFound       = -32601
	InvalidParams        = -32602
	InternalError        = -32603
	ServerNotInitialized = -32002
	UnknownErrorCode     = -32001
	RequestCancelled     = -32800
)
