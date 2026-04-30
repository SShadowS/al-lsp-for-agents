package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SShadowS/al-lsp-for-agents/al-language-server-go/wrapper"
)

func main() {
	// Define flags
	dumpClientCaps := flag.Bool("dump-client-caps", false, "Wait for initialize request, extract client capabilities as JSON, then exit")
	alExtensionPath := flag.String("al-extension-path", "", "Path to the MS AL extension directory (skips auto-discovery)")
	vscodeMode := flag.Bool("vscode", false, "VS Code mode: disable textDocumentSync to prevent interference with other extensions")
	autoDownload := flag.Bool("auto-download-al-extension", false, "Automatically download the AL extension from the VS Code Marketplace")
	alExtensionChannel := flag.String("al-extension-channel", "release", "AL extension channel: release or prerelease")
	forceUpdate := flag.Bool("force-update-al-extension", false, "Force download the latest AL extension version")
	launcher := flag.String("launcher", "", "Identifies the launching client: 'vscode', 'claude-code', or empty for unknown. Used in duplicate-instance warnings.")
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
	w.VSCodeMode = *vscodeMode
	w.AutoDownloadALExtension = *autoDownload
	w.ALExtensionChannel = *alExtensionChannel
	w.ForceUpdateALExtension = *forceUpdate
	w.Launcher = *launcher

	if err := w.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "AL LSP Wrapper error: %v\n", err)
		os.Exit(1)
	}
}
