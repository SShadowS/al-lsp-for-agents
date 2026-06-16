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
	t.Cleanup(func() { clientWritesTo.Close(); serverWritesTo.Close() })

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
	t.Cleanup(func() { cw.Close(); sw.Close() })
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

func TestMCPClientInitialize(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	c := NewMCPClient(cw, bufio.NewReader(cr), func(string, ...interface{}) {})
	t.Cleanup(func() { cw.Close(); sw.Close() })

	// Count how many initialize requests reach the fake server.
	initCount := make(chan struct{}, 4)
	go func() {
		b := bufio.NewReader(sr)
		for {
			line, err := b.ReadString('\n')
			if err != nil {
				return
			}
			var req struct {
				ID     *json.RawMessage `json:"id"`
				Method string           `json:"method"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &req) != nil {
				continue
			}
			if req.Method == "initialize" {
				initCount <- struct{}{}
			}
			if req.ID == nil {
				continue // notification (e.g. notifications/initialized)
			}
			sw.Write([]byte(`{"jsonrpc":"2.0","id":` + string(*req.ID) +
				`,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"1.0.0"}}}` + "\n"))
		}
	}()

	if err := c.Initialize("test-client"); err != nil {
		t.Fatalf("first Initialize error: %v", err)
	}
	if err := c.Initialize("test-client"); err != nil {
		t.Fatalf("second Initialize error: %v", err)
	}

	// Exactly one initialize request should have reached the server.
	<-initCount
	select {
	case <-initCount:
		t.Fatalf("second Initialize re-sent an initialize request")
	default:
	}
}
