package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// ContentBlock is one MCP tool-result content item.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolCallResult is the result field of an MCP tools/call response.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

// MCPClient speaks MCP 2024-11-05 over line-delimited JSON stdio.
type MCPClient struct {
	w   io.Writer
	r   *bufio.Reader
	log func(format string, args ...interface{})

	writeMu sync.Mutex
	id      atomic.Int64

	pendingMu sync.Mutex
	pending   map[int]chan mcpResponse

	initMu      sync.Mutex
	initialized bool

	readerOnce sync.Once
}

type mcpResponse struct {
	Result json.RawMessage
	Err    *RPCError
}

// NewMCPClient creates a client over the given writer (to server stdin) and
// reader (from server stdout). The reader goroutine starts on first request.
func NewMCPClient(w io.Writer, r *bufio.Reader, log func(string, ...interface{})) *MCPClient {
	return &MCPClient{w: w, r: r, log: log, pending: make(map[int]chan mcpResponse)}
}

func (c *MCPClient) startReader() {
	c.readerOnce.Do(func() { go c.readLoop() })
}

func (c *MCPClient) readLoop() {
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.failAll(err)
			return
		}
		line = trimLine(line)
		if line == "" {
			continue
		}
		var env struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if json.Unmarshal([]byte(line), &env) != nil || env.ID == nil {
			continue // notification or non-JSON banner line — ignore
		}
		c.pendingMu.Lock()
		ch := c.pending[*env.ID]
		delete(c.pending, *env.ID)
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- mcpResponse{Result: env.Result, Err: env.Error}
		}
	}
}

func (c *MCPClient) failAll(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[int]chan mcpResponse)
	c.pendingMu.Unlock()
	if c.log != nil && len(pending) > 0 {
		c.log("mcp: reader closed, failing %d pending request(s): %v", len(pending), err)
	}
	for _, ch := range pending {
		ch <- mcpResponse{Err: &RPCError{Code: InternalError, Message: err.Error()}}
	}
}

func (c *MCPClient) send(obj interface{}) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err = c.w.Write(b); err != nil {
		return err
	}
	_, err = c.w.Write([]byte{'\n'})
	return err
}

func (c *MCPClient) request(method string, params interface{}, timeout time.Duration) (json.RawMessage, *RPCError) {
	c.startReader()
	id := int(c.id.Add(1))

	ch := make(chan mcpResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.send(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		if c.log != nil {
			c.log("mcp: send %s failed: %v", method, err)
		}
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	select {
	case resp := <-ch:
		return resp.Result, resp.Err
	case <-time.After(timeout):
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		if c.log != nil {
			c.log("mcp: timeout waiting for %s after %s", method, timeout)
		}
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("timeout waiting for %s", method)}
	}
}

// Initialize performs the MCP handshake. It is idempotent: a second call
// returns nil without re-sending the initialize request.
func (c *MCPClient) Initialize(clientName string) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.initialized {
		return nil
	}
	_, rpcErr := c.request("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": clientName, "version": "1.0.0"},
	}, 60*time.Second)
	if rpcErr != nil {
		return fmt.Errorf("mcp initialize failed: %s", rpcErr.Message)
	}
	if err := c.send(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]interface{}{}}); err != nil {
		return err
	}
	c.initialized = true
	return nil
}

// ListTools returns the names of tools the server exposes.
func (c *MCPClient) ListTools() ([]string, error) {
	res, rpcErr := c.request("tools/list", map[string]interface{}{}, 30*time.Second)
	if rpcErr != nil {
		return nil, fmt.Errorf("tools/list failed: %s", rpcErr.Message)
	}
	var parsed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return nil, err
	}
	names := make([]string, len(parsed.Tools))
	for i, t := range parsed.Tools {
		names[i] = t.Name
	}
	return names, nil
}

// CallTool invokes an MCP tool and returns its result.
func (c *MCPClient) CallTool(name string, args interface{}) (*ToolCallResult, error) {
	res, rpcErr := c.request("tools/call", map[string]interface{}{"name": name, "arguments": args}, 60*time.Second)
	if rpcErr != nil {
		return nil, fmt.Errorf("tools/call %s: %s", name, rpcErr.Message)
	}
	var out ToolCallResult
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}
	return &out, nil
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
