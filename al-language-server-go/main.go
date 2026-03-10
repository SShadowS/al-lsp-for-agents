package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SShadowS/claude-code-lsps/al-language-server-go/wrapper"
)

func main() {
	// Define flags
	dumpClientCaps := flag.Bool("dump-client-caps", false, "Wait for initialize request, extract client capabilities as JSON, then exit")
	alExtensionPath := flag.String("al-extension-path", "", "Path to the MS AL extension directory (skips auto-discovery)")
	flag.Parse()

	// Handle --dump-client-caps mode
	if *dumpClientCaps {
		dumper := wrapper.NewCapabilityDumper()
		if err := dumper.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Normal wrapper mode
	w := wrapper.New()
	w.ALExtensionPath = *alExtensionPath

	if err := w.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "AL LSP Wrapper error: %v\n", err)
		os.Exit(1)
	}
}
