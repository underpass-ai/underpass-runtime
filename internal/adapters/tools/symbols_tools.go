package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	symbolsMaxFile   = 1024 * 1024 // 1MB max file size
	symbolsKeyStdout = "stdout"
)

// RepoSymbolsHandler extracts function, type, class, and import declarations
// from a source file WITHOUT reading the entire file content. Returns a
// structured outline that small models can use to navigate code without
// consuming their full context window.
type RepoSymbolsHandler struct {
	runner app.CommandRunner
}

func NewRepoSymbolsHandler(runner app.CommandRunner) *RepoSymbolsHandler {
	return &RepoSymbolsHandler{runner: runner}
}

func (h *RepoSymbolsHandler) Name() string {
	return "repo.symbols"
}

func (h *RepoSymbolsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path string `json:"path"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid repo.symbols args", Retryable: false}
	}
	if request.Path == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved)
	}
	return h.invokeLocal(request.Path, resolved)
}

func (h *RepoSymbolsHandler) invokeLocal(path, resolved string) (app.ToolRunResult, *domain.Error) {
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathNotExist, Retryable: false}
		}
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if info.Size() > symbolsMaxFile {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "file exceeds 1MB limit for symbol extraction", Retryable: false}
	}

	f, err := os.Open(resolved)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	defer f.Close() //nolint:errcheck

	lang := detectLanguage(path)
	symbols := extractSymbols(f, lang)

	return symbolsResult(path, lang, symbols), nil
}

func (h *RepoSymbolsHandler) invokeRemote(ctx context.Context, session domain.Session, path, resolved string) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	readResult, readErr := runShellCommand(ctx, runner, session, fmt.Sprintf("cat %s", shellQuote(resolved)), nil, symbolsMaxFile)
	if readErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(readErr, readResult.Output)
	}

	lang := detectLanguage(path)
	reader := strings.NewReader(readResult.Output)
	symbols := extractSymbols(reader, lang)

	return symbolsResult(path, lang, symbols), nil
}

type sourceSymbol struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Line int    `json:"line"`
	Sig  string `json:"signature,omitempty"`
}

func symbolsResult(path, lang string, symbols []sourceSymbol) app.ToolRunResult {
	return app.ToolRunResult{
		Output: map[string]any{
			"path":     path,
			"language": lang,
			"symbols":  symbols,
			"count":    len(symbols),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: symbolsKeyStdout, Message: fmt.Sprintf("extracted %d symbols from %s", len(symbols), path)}},
	}
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	default:
		return "unknown"
	}
}

// Language-specific regex patterns for symbol extraction.
var symbolPatterns = map[string][]*symbolPattern{
	"go": {
		{kind: "package", re: regexp.MustCompile(`^package\s+(\w+)`)},
		{kind: "import", re: regexp.MustCompile(`^\s*"([^"]+)"`)},
		{kind: "function", re: regexp.MustCompile(`^func\s+(\([^)]*\)\s*)?(\w+)\s*\(([^)]*)\)`)},
		{kind: "type", re: regexp.MustCompile(`^type\s+(\w+)\s+(struct|interface)`)},
		{kind: "const", re: regexp.MustCompile(`^const\s+(\w+)`)},
		{kind: "var", re: regexp.MustCompile(`^var\s+(\w+)`)},
	},
	"python": {
		{kind: "import", re: regexp.MustCompile(`^(?:from\s+\S+\s+)?import\s+(.+)`)},
		{kind: "class", re: regexp.MustCompile(`^class\s+(\w+)`)},
		{kind: "function", re: regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\)`)},
	},
	"javascript": {
		{kind: "import", re: regexp.MustCompile(`^import\s+`)},
		{kind: "function", re: regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+(\w+)`)},
		{kind: "class", re: regexp.MustCompile(`^(?:export\s+)?class\s+(\w+)`)},
		{kind: "const", re: regexp.MustCompile(`^(?:export\s+)?const\s+(\w+)`)},
	},
	"typescript": {
		{kind: "import", re: regexp.MustCompile(`^import\s+`)},
		{kind: "function", re: regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+(\w+)`)},
		{kind: "class", re: regexp.MustCompile(`^(?:export\s+)?class\s+(\w+)`)},
		{kind: "interface", re: regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`)},
		{kind: "type", re: regexp.MustCompile(`^(?:export\s+)?type\s+(\w+)`)},
		{kind: "const", re: regexp.MustCompile(`^(?:export\s+)?const\s+(\w+)`)},
	},
	"rust": {
		{kind: "use", re: regexp.MustCompile(`^use\s+(.+);`)},
		{kind: "function", re: regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)},
		{kind: "struct", re: regexp.MustCompile(`^(?:pub\s+)?struct\s+(\w+)`)},
		{kind: "enum", re: regexp.MustCompile(`^(?:pub\s+)?enum\s+(\w+)`)},
		{kind: "trait", re: regexp.MustCompile(`^(?:pub\s+)?trait\s+(\w+)`)},
		{kind: "impl", re: regexp.MustCompile(`^impl\s+(?:<[^>]+>\s+)?(\w+)`)},
	},
	"java": {
		{kind: "import", re: regexp.MustCompile(`^import\s+(.+);`)},
		{kind: "class", re: regexp.MustCompile(`^(?:public\s+)?(?:abstract\s+)?class\s+(\w+)`)},
		{kind: "interface", re: regexp.MustCompile(`^(?:public\s+)?interface\s+(\w+)`)},
		{kind: "method", re: regexp.MustCompile(`^\s+(?:public|private|protected)?\s*(?:static\s+)?(?:\w+\s+)(\w+)\s*\(`)},
	},
}

type symbolPattern struct {
	kind string
	re   *regexp.Regexp
}

func extractSymbols(reader io.Reader, lang string) []sourceSymbol {
	patterns, ok := symbolPatterns[lang]
	if !ok {
		// Fallback: generic patterns for unknown languages.
		patterns = []*symbolPattern{
			{kind: "function", re: regexp.MustCompile(`^(?:func|def|function|fn|pub fn|async def)\s+(\w+)`)},
			{kind: "class", re: regexp.MustCompile(`^(?:class|struct|type|interface|enum|trait)\s+(\w+)`)},
		}
	}

	var symbols []sourceSymbol
	scanner := bufio.NewScanner(reader)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		for _, p := range patterns {
			matches := p.re.FindStringSubmatch(line)
			if matches == nil {
				continue
			}

			name := ""
			sig := strings.TrimSpace(line)
			// For Go methods, the name is in group 2 (after receiver).
			if lang == "go" && p.kind == "function" && len(matches) > 2 {
				name = matches[2]
			} else if len(matches) > 1 {
				name = matches[1]
			}

			symbols = append(symbols, sourceSymbol{
				Kind: p.kind,
				Name: name,
				Line: lineNum,
				Sig:  truncateSig(sig, 120),
			})
			break // One match per line.
		}
	}

	return symbols
}

func truncateSig(sig string, maxLen int) string {
	if len(sig) <= maxLen {
		return sig
	}
	return sig[:maxLen] + "..."
}
