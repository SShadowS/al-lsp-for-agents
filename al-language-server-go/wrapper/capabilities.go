package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// CapabilityDumper handles the --dump-client-caps mode
type CapabilityDumper struct {
	reader *bufio.Reader
	writer io.Writer
	logFn  func(format string, args ...interface{})
}

// NewCapabilityDumper creates a new capability dumper
func NewCapabilityDumper() *CapabilityDumper {
	return &CapabilityDumper{
		reader: bufio.NewReader(os.Stdin),
		writer: os.Stdout,
		logFn: func(format string, args ...interface{}) {
			// Default: log to stderr so stdout is clean for JSON output
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	}
}

// Run waits for an initialize request, extracts capabilities, and prints them
func (d *CapabilityDumper) Run() error {
	d.logFn("Waiting for initialize request...")

	// Read the first message (should be initialize)
	msg, err := ReadMessage(d.reader)
	if err != nil {
		if err == io.EOF {
			return fmt.Errorf("client disconnected before sending initialize")
		}
		return fmt.Errorf("failed to read message: %w", err)
	}

	if msg.Method != "initialize" {
		return fmt.Errorf("expected initialize request, got: %s", msg.Method)
	}

	d.logFn("Received initialize request")

	// Extract and output capabilities
	capsJSON, err := d.ExtractCapabilities(msg.Params)
	if err != nil {
		return err
	}

	// Write to stdout
	fmt.Fprintln(d.writer, string(capsJSON))

	d.logFn("Capabilities extracted successfully")
	return nil
}

// ExtractCapabilities extracts client capabilities from initialize params
func (d *CapabilityDumper) ExtractCapabilities(params json.RawMessage) ([]byte, error) {
	// Parse the initialize params to get just the capabilities
	var initParams struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}

	if err := json.Unmarshal(params, &initParams); err != nil {
		return nil, fmt.Errorf("failed to parse initialize params: %w", err)
	}

	if initParams.Capabilities == nil {
		return nil, fmt.Errorf("no capabilities found in initialize params")
	}

	// Re-parse to get a clean object we can pretty-print
	var caps interface{}
	if err := json.Unmarshal(initParams.Capabilities, &caps); err != nil {
		return nil, fmt.Errorf("failed to parse capabilities: %w", err)
	}

	// Pretty-print with indentation
	output, err := json.MarshalIndent(caps, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	return output, nil
}

// ExtractClientCapabilitiesFromInitialize is a utility function to extract
// capabilities from a raw initialize params JSON
func ExtractClientCapabilitiesFromInitialize(rawParams json.RawMessage) (map[string]interface{}, error) {
	var initParams struct {
		Capabilities map[string]interface{} `json:"capabilities"`
	}

	if err := json.Unmarshal(rawParams, &initParams); err != nil {
		return nil, fmt.Errorf("failed to parse initialize params: %w", err)
	}

	if initParams.Capabilities == nil {
		return nil, fmt.Errorf("no capabilities found")
	}

	return initParams.Capabilities, nil
}
