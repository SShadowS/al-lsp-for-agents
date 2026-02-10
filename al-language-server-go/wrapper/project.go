package wrapper

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FindAppJSON searches for app.json starting from the given directory,
// searching up to maxDepth parent directories
func FindAppJSON(startDir string, maxDepth int) string {
	dir := startDir

	for i := 0; i < maxDepth; i++ {
		appJsonPath := filepath.Join(dir, "app.json")
		if _, err := os.Stat(appJsonPath); err == nil {
			return appJsonPath
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			break
		}
		dir = parent
	}

	return ""
}

// GetProjectRoot determines the AL project root for a given file path
// by finding the directory containing app.json
func GetProjectRoot(filePath string) string {
	dir := filepath.Dir(filePath)
	appJson := FindAppJSON(dir, 5)

	if appJson == "" {
		return ""
	}

	return filepath.Dir(appJson)
}

// AppManifest represents the parsed content of app.json
type AppManifest struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Publisher    string          `json:"publisher"`
	Version      string          `json:"version"`
	Dependencies []AppDependency `json:"dependencies"`
	Raw          string          `json:"-"` // original file content for al/loadManifest
}

// AppDependency represents a dependency entry in app.json
type AppDependency struct {
	ID        string `json:"id"`
	AppID     string `json:"appId"`
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	Version   string `json:"version"`
}

// GetAppID returns the app ID, checking both "id" and "appId" fields
// (app.json uses both conventions)
func (d *AppDependency) GetAppID() string {
	if d.ID != "" {
		return d.ID
	}
	return d.AppID
}

// ProjectReferenceDefinition represents a dependency for workspace settings
type ProjectReferenceDefinition struct {
	AppID     string `json:"appId"`
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	Version   string `json:"version"`
}

// LoadManifestParams represents parameters for al/loadManifest
type LoadManifestParams struct {
	ProjectFolder string `json:"projectFolder"`
	Manifest      string `json:"manifest"`
}

// ParseAppManifest reads and parses app.json from the given path
func ParseAppManifest(appJsonPath string) *AppManifest {
	data, err := os.ReadFile(appJsonPath)
	if err != nil {
		return nil
	}

	var manifest AppManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}

	manifest.Raw = string(data)
	return &manifest
}

// ToProjectReferenceDefinitions converts manifest dependencies to workspace settings format
func (m *AppManifest) ToProjectReferenceDefinitions() []ProjectReferenceDefinition {
	if m == nil || len(m.Dependencies) == 0 {
		return []ProjectReferenceDefinition{}
	}

	refs := make([]ProjectReferenceDefinition, len(m.Dependencies))
	for i, dep := range m.Dependencies {
		refs[i] = ProjectReferenceDefinition{
			AppID:     dep.GetAppID(),
			Name:      dep.Name,
			Publisher: dep.Publisher,
			Version:   dep.Version,
		}
	}
	return refs
}

// WorkspaceSettings represents AL workspace configuration
type WorkspaceSettings struct {
	WorkspacePath                     string                        `json:"workspacePath"`
	ALResourceConfigurationSettings   ALResourceConfigurationSettings `json:"alResourceConfigurationSettings"`
	SetActiveWorkspace                bool                          `json:"setActiveWorkspace"`
	DependencyParentWorkspacePath     *string                       `json:"dependencyParentWorkspacePath"`
	ExpectedProjectReferenceDefinitions []ProjectReferenceDefinition `json:"expectedProjectReferenceDefinitions"`
	ActiveWorkspaceClosure            []string                      `json:"activeWorkspaceClosure"`
}

// ALResourceConfigurationSettings represents AL-specific settings
type ALResourceConfigurationSettings struct {
	AssemblyProbingPaths     []string `json:"assemblyProbingPaths"`
	CodeAnalyzers            []string `json:"codeAnalyzers"`
	EnableCodeAnalysis       bool     `json:"enableCodeAnalysis"`
	BackgroundCodeAnalysis   string   `json:"backgroundCodeAnalysis"`
	PackageCachePaths        []string `json:"packageCachePaths"`
	RuleSetPath              *string  `json:"ruleSetPath"`
	EnableCodeActions        bool     `json:"enableCodeActions"`
	IncrementalBuild         bool     `json:"incrementalBuild"`
	OutputAnalyzerStatistics bool     `json:"outputAnalyzerStatistics"`
	EnableExternalRulesets   bool     `json:"enableExternalRulesets"`
}

// NewWorkspaceSettings creates workspace settings for the given project root
func NewWorkspaceSettings(projectRoot string, manifest *AppManifest) *WorkspaceSettings {
	var refs []ProjectReferenceDefinition
	if manifest != nil {
		refs = manifest.ToProjectReferenceDefinitions()
	} else {
		refs = []ProjectReferenceDefinition{}
	}

	return &WorkspaceSettings{
		WorkspacePath: projectRoot,
		ALResourceConfigurationSettings: ALResourceConfigurationSettings{
			AssemblyProbingPaths:     []string{"./.netpackages"},
			CodeAnalyzers:            []string{},
			EnableCodeAnalysis:       false,
			BackgroundCodeAnalysis:   "Project",
			PackageCachePaths:        []string{"./.alpackages"},
			RuleSetPath:              nil,
			EnableCodeActions:        true,
			IncrementalBuild:         false,
			OutputAnalyzerStatistics: true,
			EnableExternalRulesets:   true,
		},
		SetActiveWorkspace:                  true,
		DependencyParentWorkspacePath:       nil,
		ExpectedProjectReferenceDefinitions: refs,
		ActiveWorkspaceClosure:              []string{projectRoot},
	}
}

// ActiveWorkspaceParams represents parameters for al/setActiveWorkspace
type ActiveWorkspaceParams struct {
	CurrentWorkspaceFolderPath WorkspaceFolderPath `json:"currentWorkspaceFolderPath"`
	Settings                   *WorkspaceSettings  `json:"settings"`
}

// WorkspaceFolderPath represents a workspace folder
type WorkspaceFolderPath struct {
	URI   string `json:"uri"`
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// NewActiveWorkspaceParams creates parameters for al/setActiveWorkspace
func NewActiveWorkspaceParams(projectRoot string, manifest *AppManifest) *ActiveWorkspaceParams {
	return &ActiveWorkspaceParams{
		CurrentWorkspaceFolderPath: WorkspaceFolderPath{
			URI:   PathToFileURI(projectRoot),
			Name:  filepath.Base(projectRoot),
			Index: 0,
		},
		Settings: NewWorkspaceSettings(projectRoot, manifest),
	}
}

// DidOpenTextDocumentParams represents textDocument/didOpen parameters
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// TextDocumentItem represents a text document
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// NewDidOpenParams creates parameters for textDocument/didOpen
func NewDidOpenParams(filePath string, content string) *DidOpenTextDocumentParams {
	languageID := "al"
	if filepath.Ext(filePath) == ".json" {
		languageID = "json"
	}

	return &DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        PathToFileURI(filePath),
			LanguageID: languageID,
			Version:    1,
			Text:       content,
		},
	}
}

// DidChangeConfigurationParams represents workspace/didChangeConfiguration parameters
type DidChangeConfigurationParams struct {
	Settings *WorkspaceSettings `json:"settings"`
}

// InitializeParams represents LSP initialize request parameters
type InitializeParams struct {
	ProcessID             int                  `json:"processId"`
	RootURI               string               `json:"rootUri,omitempty"`
	Capabilities          ClientCapabilities   `json:"capabilities"`
	Trace                 string               `json:"trace,omitempty"`
	WorkspaceFolders      []WorkspaceFolder    `json:"workspaceFolders,omitempty"`
	InitializationOptions map[string]any       `json:"initializationOptions,omitempty"`
}

// ClientCapabilities represents client capabilities
type ClientCapabilities struct {
	Workspace    WorkspaceCapabilities    `json:"workspace,omitempty"`
	TextDocument TextDocumentCapabilities `json:"textDocument,omitempty"`
	Window       WindowCapabilities       `json:"window,omitempty"`
	General      *GeneralCapabilities     `json:"general,omitempty"`
}

// GeneralCapabilities represents general client capabilities
type GeneralCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// WorkspaceCapabilities represents workspace-related capabilities
type WorkspaceCapabilities struct {
	ApplyEdit              bool                   `json:"applyEdit,omitempty"`
	WorkspaceEdit          WorkspaceEditCapability `json:"workspaceEdit,omitempty"`
	DidChangeConfiguration DynamicRegistration    `json:"didChangeConfiguration,omitempty"`
	DidChangeWatchedFiles  DynamicRegistration    `json:"didChangeWatchedFiles,omitempty"`
	Symbol                 DynamicRegistration    `json:"symbol,omitempty"`
	ExecuteCommand         DynamicRegistration    `json:"executeCommand,omitempty"`
	Configuration          bool                   `json:"configuration,omitempty"`
	WorkspaceFolders       bool                   `json:"workspaceFolders,omitempty"`
}

// WorkspaceEditCapability represents workspace edit capabilities
type WorkspaceEditCapability struct {
	DocumentChanges bool `json:"documentChanges,omitempty"`
}

// DynamicRegistration represents dynamic registration capability
type DynamicRegistration struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// TextDocumentCapabilities represents text document capabilities
type TextDocumentCapabilities struct {
	Synchronization    TextDocumentSyncCapability    `json:"synchronization,omitempty"`
	Completion         CompletionCapability          `json:"completion,omitempty"`
	Hover              HoverCapability               `json:"hover,omitempty"`
	SignatureHelp      DynamicRegistration           `json:"signatureHelp,omitempty"`
	Definition         DefinitionCapability          `json:"definition,omitempty"`
	References         DynamicRegistration           `json:"references,omitempty"`
	DocumentHighlight  DynamicRegistration           `json:"documentHighlight,omitempty"`
	DocumentSymbol     DocumentSymbolCapability      `json:"documentSymbol,omitempty"`
	CodeAction         DynamicRegistration           `json:"codeAction,omitempty"`
	CodeLens           DynamicRegistration           `json:"codeLens,omitempty"`
	Formatting         DynamicRegistration           `json:"formatting,omitempty"`
	RangeFormatting    DynamicRegistration           `json:"rangeFormatting,omitempty"`
	OnTypeFormatting   DynamicRegistration           `json:"onTypeFormatting,omitempty"`
	Rename             DynamicRegistration           `json:"rename,omitempty"`
	DocumentLink       DynamicRegistration           `json:"documentLink,omitempty"`
	CallHierarchy      CallHierarchyCapability       `json:"callHierarchy,omitempty"`
	PublishDiagnostics PublishDiagnosticsCapability   `json:"publishDiagnostics,omitempty"`
}

// HoverCapability represents hover capabilities
type HoverCapability struct {
	DynamicRegistration bool     `json:"dynamicRegistration,omitempty"`
	ContentFormat       []string `json:"contentFormat,omitempty"`
}

// DefinitionCapability represents definition capabilities
type DefinitionCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
	LinkSupport         bool `json:"linkSupport,omitempty"`
}

// DocumentSymbolCapability represents document symbol capabilities
type DocumentSymbolCapability struct {
	DynamicRegistration               bool `json:"dynamicRegistration,omitempty"`
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// CallHierarchyCapability represents call hierarchy capabilities
type CallHierarchyCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// TextDocumentSyncCapability represents text document sync capabilities
type TextDocumentSyncCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
	WillSave            bool `json:"willSave,omitempty"`
	WillSaveWaitUntil   bool `json:"willSaveWaitUntil,omitempty"`
	DidSave             bool `json:"didSave,omitempty"`
}

// CompletionCapability represents completion capabilities
type CompletionCapability struct {
	DynamicRegistration bool                     `json:"dynamicRegistration,omitempty"`
	CompletionItem      CompletionItemCapability `json:"completionItem,omitempty"`
}

// CompletionItemCapability represents completion item capabilities
type CompletionItemCapability struct {
	SnippetSupport bool `json:"snippetSupport,omitempty"`
}

// PublishDiagnosticsCapability represents publish diagnostics capabilities
type PublishDiagnosticsCapability struct {
	RelatedInformation     bool                    `json:"relatedInformation,omitempty"`
	TagSupport             *DiagnosticTagSupport   `json:"tagSupport,omitempty"`
	VersionSupport         bool                    `json:"versionSupport,omitempty"`
	CodeDescriptionSupport bool                    `json:"codeDescriptionSupport,omitempty"`
	DataSupport            bool                    `json:"dataSupport,omitempty"`
}

// DiagnosticTagSupport represents diagnostic tag support
type DiagnosticTagSupport struct {
	ValueSet []int `json:"valueSet,omitempty"`
}

// WindowCapabilities represents window capabilities
type WindowCapabilities struct {
	ShowMessage      ShowMessageRequestCapability `json:"showMessage,omitempty"`
	WorkDoneProgress bool                         `json:"workDoneProgress,omitempty"`
}

// ShowMessageRequestCapability represents show message request capabilities
type ShowMessageRequestCapability struct {
	MessageActionItem MessageActionItemCapability `json:"messageActionItem,omitempty"`
}

// MessageActionItemCapability represents message action item capabilities
type MessageActionItemCapability struct {
	AdditionalPropertiesSupport bool `json:"additionalPropertiesSupport,omitempty"`
}

// WorkspaceFolder represents a workspace folder
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// NewInitializeParams creates initialize parameters
func NewInitializeParams(workspaceRoot string) *InitializeParams {
	return &InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   PathToFileURI(workspaceRoot),
		Capabilities: ClientCapabilities{
			Workspace: WorkspaceCapabilities{
				ApplyEdit: true,
				WorkspaceEdit: WorkspaceEditCapability{
					DocumentChanges: true,
				},
				DidChangeConfiguration: DynamicRegistration{DynamicRegistration: true},
				DidChangeWatchedFiles:  DynamicRegistration{DynamicRegistration: true},
				Symbol:                 DynamicRegistration{DynamicRegistration: true},
				ExecuteCommand:         DynamicRegistration{DynamicRegistration: true},
				Configuration:          true,
				WorkspaceFolders:       true,
			},
			TextDocument: TextDocumentCapabilities{
				Synchronization: TextDocumentSyncCapability{
					DynamicRegistration: true,
					WillSave:            true,
					WillSaveWaitUntil:   true,
					DidSave:             true,
				},
				Completion: CompletionCapability{
					DynamicRegistration: true,
					CompletionItem: CompletionItemCapability{
						SnippetSupport: true,
					},
				},
				Hover:             HoverCapability{DynamicRegistration: true, ContentFormat: []string{"markdown", "plaintext"}},
				SignatureHelp:     DynamicRegistration{DynamicRegistration: true},
				Definition:        DefinitionCapability{DynamicRegistration: true, LinkSupport: true},
				References:        DynamicRegistration{DynamicRegistration: true},
				DocumentHighlight: DynamicRegistration{DynamicRegistration: true},
				DocumentSymbol:    DocumentSymbolCapability{DynamicRegistration: true, HierarchicalDocumentSymbolSupport: true},
				CodeAction:        DynamicRegistration{DynamicRegistration: true},
				CodeLens:          DynamicRegistration{DynamicRegistration: true},
				Formatting:        DynamicRegistration{DynamicRegistration: true},
				RangeFormatting:   DynamicRegistration{DynamicRegistration: true},
				OnTypeFormatting:  DynamicRegistration{DynamicRegistration: true},
				Rename:            DynamicRegistration{DynamicRegistration: true},
				DocumentLink:      DynamicRegistration{DynamicRegistration: true},
				CallHierarchy:     CallHierarchyCapability{DynamicRegistration: true},
				PublishDiagnostics: PublishDiagnosticsCapability{
					RelatedInformation:     true,
					TagSupport:             &DiagnosticTagSupport{ValueSet: []int{1, 2}},
					VersionSupport:         true,
					CodeDescriptionSupport: true,
					DataSupport:            true,
				},
			},
			Window: WindowCapabilities{
				ShowMessage: ShowMessageRequestCapability{
					MessageActionItem: MessageActionItemCapability{
						AdditionalPropertiesSupport: true,
					},
				},
				WorkDoneProgress: true,
			},
		},
		Trace: "verbose",
		WorkspaceFolders: []WorkspaceFolder{
			{
				URI:  PathToFileURI(workspaceRoot),
				Name: filepath.Base(workspaceRoot),
			},
		},
	}
}
