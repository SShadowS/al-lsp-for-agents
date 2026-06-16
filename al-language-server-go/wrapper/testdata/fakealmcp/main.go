package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	r := bufio.NewReader(os.Stdin)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var req struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
		}
		if json.Unmarshal([]byte(line), &req) != nil || req.ID == nil {
			continue
		}
		id := string(*req.ID)
		var resp string
		switch req.Method {
		case "initialize":
			resp = `{"jsonrpc":"2.0","id":` + id + `,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"0"}}}`
		case "tools/list":
			resp = `{"jsonrpc":"2.0","id":` + id + `,"result":{"tools":[{"name":"al_symbolrelations"},{"name":"al_addproject"}]}}`
		case "tools/call":
			resp = `{"jsonrpc":"2.0","id":` + id + `,"result":{"content":[{"type":"text","text":"{\"relations\":[]}"}],"isError":false}}`
		default:
			resp = `{"jsonrpc":"2.0","id":` + id + `,"result":null}`
		}
		fmt.Fprintf(w, "%s\n", resp)
		w.Flush()
	}
}
