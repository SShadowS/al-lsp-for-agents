package wrapper

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// fakeServer drives an MCPClient over in-memory pipes, acting as almcp.
func TestMCPClientCallTool(t *testing.T) {
	clientReadsFrom, serverWritesTo := io.Pipe() // server -> client
	serverReadsFrom, clientWritesTo := io.Pipe() // client -> server

	c := NewMCPClient(clientWritesTo, bufio.NewReader(clientReadsFrom), func(string, ...interface{}) {})

	// Fake server: reply to every request id with a tool result.
	go func() {
		sr := bufio.NewReader(serverReadsFrom)
		for {
			line, err := sr.ReadString('\n')
			if err != nil {
				return
			}
			var req map[string]json.RawMessage
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &req) != nil {
				continue
			}
			if _, ok := req["id"]; !ok {
				continue // notification (initialized)
			}
			resp := `{"jsonrpc":"2.0","id":` + string(req["id"]) +
				`,"result":{"content":[{"type":"text","text":"{\"relations\":[]}"}],"isError":false}}` + "\n"
			serverWritesTo.Write([]byte(resp))
		}
	}()

	res, err := c.CallTool("al_symbolrelations", map[string]interface{}{"parameters": map[string]string{"symbolName": "Customer"}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected isError false")
	}
	if len(res.Content) != 1 || res.Content[0].Text != `{"relations":[]}` {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
}

func TestMCPClientCallToolError(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	c := NewMCPClient(cw, bufio.NewReader(cr), func(string, ...interface{}) {})
	go func() {
		b := bufio.NewReader(sr)
		for {
			line, err := b.ReadString('\n')
			if err != nil {
				return
			}
			var req map[string]json.RawMessage
			json.Unmarshal([]byte(strings.TrimSpace(line)), &req)
			if _, ok := req["id"]; !ok {
				continue
			}
			sw.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req["id"]) +
				`,"error":{"code":-32602,"message":"Unknown tool: 'al_inspectpage'"}}` + "\n"))
		}
	}()
	_, err := c.CallTool("al_inspectpage", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "Unknown tool") {
		t.Fatalf("expected Unknown tool error, got %v", err)
	}
}
