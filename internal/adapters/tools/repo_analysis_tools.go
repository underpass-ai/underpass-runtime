package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	repoAnalysisKeyStdout         = "stdout"
	repoAnalysisContentTypeJSON   = "application/json"
	repoAnalysisKeyWorkingTree    = "working_tree"
	repoAnalysisKeyProvidedOutput = "provided_output"
	repoAnalysisKeyCaseSensitive  = "case_sensitive"
	repoAnalysisKeyBaseRefDiff    = "base_ref_diff"
	repoAnalysisKeyBaseRef        = "base_ref"
)

var (
	goFailPattern        = regexp.MustCompile(`^--- FAIL: ([^\s]+)`)
	pytestFailPattern    = regexp.MustCompile(`^FAILED\s+(.+?)(?:\s+-\s+.*)?$`)
	cargoFailPattern     = regexp.MustCompile(`^test\s+([^\s]+)\s+\.\.\.\s+FAILED$`)
	jestFailPattern      = regexp.MustCompile(`^(?:FAIL|✕)\s+(.+)$`)
	goPackageFailPattern = regexp.MustCompile(`^FAIL\s+([^\s]+)\s+`)
	pythonFramePattern   = regexp.MustCompile(`^File\s+".+",\s+line\s+\d+`)
	goFramePattern       = regexp.MustCompile(`^[^\s:][^:]*:\d+(?::\d+)?`)
	threadPanicPattern   = regexp.MustCompile(`^thread\s+'.*'\s+panicked`)
)

type RepoTestFailuresSummaryHandler struct {
	runner app.CommandRunner
}

type RepoStacktraceSummaryHandler struct {
	runner app.CommandRunner
}

type RepoChangedFilesHandler struct {
	runner app.CommandRunner
}

type RepoSymbolSearchHandler struct {
	runner app.CommandRunner
}

type RepoFindReferencesHandler struct {
	runner app.CommandRunner
}

type summarizedFailure struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Line string `json:"line,omitempty"`
}

type summarizedStacktrace struct {
	Type       string   `json:"type"`
	Message    string   `json:"message"`
	Frames     []string `json:"frames"`
	FrameCount int      `json:"frame_count"`
}

type repoChangedFile struct {
	Path           string `json:"path"`
	Status         string `json:"status"`
	IndexStatus    string `json:"index_status,omitempty"`
	WorktreeStatus string `json:"worktree_status,omitempty"`
	Staged         bool   `json:"staged"`
	Unstaged       bool   `json:"unstaged"`
	Untracked      bool   `json:"untracked"`
	Deleted        bool   `json:"deleted"`
	RenamedFrom    string `json:"renamed_from,omitempty"`
}

type repoSymbolMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Snippet string `json:"snippet"`
}

type repoReferenceMatch struct {
	Path          string `json:"path"`
	Line          int    `json:"line"`
	Column        int    `json:"column"`
	Snippet       string `json:"snippet"`
	IsDeclaration bool   `json:"is_declaration"`
}

func NewRepoTestFailuresSummaryHandler(runner app.CommandRunner) *RepoTestFailuresSummaryHandler {
	return &RepoTestFailuresSummaryHandler{runner: runner}
}

func NewRepoStacktraceSummaryHandler(runner app.CommandRunner) *RepoStacktraceSummaryHandler {
	return &RepoStacktraceSummaryHandler{runner: runner}
}

func NewRepoChangedFilesHandler(runner app.CommandRunner) *RepoChangedFilesHandler {
	return &RepoChangedFilesHandler{runner: runner}
}

func NewRepoSymbolSearchHandler(runner app.CommandRunner) *RepoSymbolSearchHandler {
	return &RepoSymbolSearchHandler{runner: runner}
}

func NewRepoFindReferencesHandler(runner app.CommandRunner) *RepoFindReferencesHandler {
	return &RepoFindReferencesHandler{runner: runner}
}

func (h *RepoTestFailuresSummaryHandler) Name() string {
	return "repo.test_failures_summary"
}

func (h *RepoStacktraceSummaryHandler) Name() string {
	return "repo.stacktrace_summary"
}

func (h *RepoChangedFilesHandler) Name() string {
	return "repo.changed_files"
}

func (h *RepoSymbolSearchHandler) Name() string {
	return "repo.symbol_search"
}

func (h *RepoFindReferencesHandler) Name() string {
	return "repo.find_references"
}

func (h *RepoTestFailuresSummaryHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target         string   `json:"target"`
		ExtraArgs      []string `json:"extra_args"`
		Output         string   `json:"output"`
		MaxFailures    int      `json:"max_failures"`
		MaxDiagnostics int      `json:"max_diagnostics"`
	}{
		MaxFailures:    30,
		MaxDiagnostics: 30,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.test_failures_summary args",
				Retryable: false,
			}
		}
	}

	maxFailures := clampInt(request.MaxFailures, 1, 200, 30)
	maxDiagnostics := clampInt(request.MaxDiagnostics, 1, 200, 30)

	source := repoAnalysisKeyProvidedOutput
	projectTypeName := "unknown"
	command := []string{}
	exitCode := 0
	rawOutput := strings.TrimSpace(request.Output)

	if rawOutput == "" {
		source = "test_run"
		run, runErr := runRepoTestsForAnalysis(ctx, ensureRunner(h.runner), session, request.Target, request.ExtraArgs)
		if runErr != nil {
			return app.ToolRunResult{}, runErr
		}
		projectTypeName = run.ProjectType.Name
		command = run.Command
		exitCode = run.Result.ExitCode
		rawOutput = strings.TrimSpace(run.Result.Output)
	}

	failures := summarizeTestFailures(rawOutput, maxFailures)
	diagnostics := extractDiagnostics(rawOutput, maxDiagnostics)
	outputExcerpt := rawOutput
	if len(outputExcerpt) > 4096 {
		outputExcerpt = outputExcerpt[:4096] + "\n[truncated]"
	}

	summary := fmt.Sprintf("identified %d failing tests", len(failures))
	resultOutput := map[string]any{
		"project_type":   projectTypeName,
		"source":         source,
		"command":        command,
		"exit_code":      exitCode,
		"failed_count":   len(failures),
		"failed_tests":   failures,
		"diagnostics":    diagnostics,
		"output_excerpt": outputExcerpt,
		"summary":        summary,
		"output":         summary,
	}

	reportBytes, marshalErr := json.MarshalIndent(resultOutput, "", "  ")
	artifacts := []app.ArtifactPayload{}
	if marshalErr == nil {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "test-failures-summary.json",
			ContentType: repoAnalysisContentTypeJSON,
			Data:        reportBytes,
		})
	}

	return app.ToolRunResult{
		ExitCode:  exitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: repoAnalysisKeyStdout, Message: summary}},
		Output:    resultOutput,
		Artifacts: artifacts,
	}, nil
}

func (h *RepoStacktraceSummaryHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target    string   `json:"target"`
		ExtraArgs []string `json:"extra_args"`
		Output    string   `json:"output"`
		MaxTraces int      `json:"max_traces"`
		MaxFrames int      `json:"max_frames"`
	}{
		MaxTraces: 10,
		MaxFrames: 20,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.stacktrace_summary args",
				Retryable: false,
			}
		}
	}

	maxTraces := clampInt(request.MaxTraces, 1, 50, 10)
	maxFrames := clampInt(request.MaxFrames, 1, 80, 20)

	source := repoAnalysisKeyProvidedOutput
	projectTypeName := "unknown"
	command := []string{}
	exitCode := 0
	rawOutput := strings.TrimSpace(request.Output)

	if rawOutput == "" {
		source = "test_run"
		run, runErr := runRepoTestsForAnalysis(ctx, ensureRunner(h.runner), session, request.Target, request.ExtraArgs)
		if runErr != nil {
			return app.ToolRunResult{}, runErr
		}
		projectTypeName = run.ProjectType.Name
		command = run.Command
		exitCode = run.Result.ExitCode
		rawOutput = strings.TrimSpace(run.Result.Output)
	}

	traces := summarizeStacktraces(rawOutput, maxTraces, maxFrames)
	diagnostics := extractDiagnostics(rawOutput, 40)
	summary := fmt.Sprintf("identified %d stacktraces", len(traces))

	resultOutput := map[string]any{
		"project_type": projectTypeName,
		"source":       source,
		"command":      command,
		"exit_code":    exitCode,
		"trace_count":  len(traces),
		"stacktraces":  traces,
		"diagnostics":  diagnostics,
		"summary":      summary,
		"output":       summary,
	}

	reportBytes, marshalErr := json.MarshalIndent(resultOutput, "", "  ")
	artifacts := []app.ArtifactPayload{}
	if marshalErr == nil {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "stacktrace-summary.json",
			ContentType: repoAnalysisContentTypeJSON,
			Data:        reportBytes,
		})
	}

	return app.ToolRunResult{
		ExitCode:  exitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: repoAnalysisKeyStdout, Message: summary}},
		Output:    resultOutput,
		Artifacts: artifacts,
	}, nil
}

func (h *RepoChangedFilesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		BaseRef          string `json:"base_ref"`
		Path             string `json:"path"`
		MaxFiles         int    `json:"max_files"`
		IncludeUntracked bool   `json:"include_untracked"`
		Output           string `json:"output"`
	}{
		Path:             ".",
		MaxFiles:         200,
		IncludeUntracked: true,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.changed_files args",
				Retryable: false,
			}
		}
	}

	path, pathErr := sanitizeRelativePath(request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   pathErr.Error(),
			Retryable: false,
		}
	}
	if path == "" {
		path = "."
	}
	maxFiles := clampInt(request.MaxFiles, 1, 2000, 200)

	source := repoAnalysisKeyProvidedOutput
	mode := repoAnalysisKeyProvidedOutput
	command := []string{}
	exitCode := 0
	rawOutput := strings.TrimSpace(request.Output)

	if rawOutput == "" {
		source = "git"
		runner := ensureRunner(h.runner)
		baseRef := strings.TrimSpace(request.BaseRef)
		commandArgs := []string{"status", "--porcelain=v1"}
		mode = repoAnalysisKeyWorkingTree
		if baseRef != "" {
			commandArgs = []string{"diff", "--name-status", "--find-renames", baseRef + "...HEAD"}
			mode = repoAnalysisKeyBaseRefDiff
		}
		if path != "." {
			commandArgs = append(commandArgs, "--", path)
		}
		commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "git",
			Args:     commandArgs,
			MaxBytes: 1024 * 1024,
		})
		command = append([]string{"git"}, commandArgs...)
		exitCode = commandResult.ExitCode
		rawOutput = strings.TrimSpace(commandResult.Output)
		if runErr != nil {
			return app.ToolRunResult{
				ExitCode: exitCode,
				Logs: []domain.LogLine{{
					At:      time.Now().UTC(),
					Channel: repoAnalysisKeyStdout,
					Message: commandResult.Output,
				}},
				Output: map[string]any{
					"source":    source,
					"mode":              mode,
					repoAnalysisKeyBaseRef: baseRef,
					"path":              path,
					"command":   command,
					"exit_code": exitCode,
				},
			}, toGitToolError(runErr, commandResult.ExitCode, commandResult.Output)
		}
	}

	changed := summarizeChangedFiles(rawOutput, mode, request.IncludeUntracked, maxFiles)
	summary := fmt.Sprintf("identified %d changed files", len(changed))
	resultOutput := map[string]any{
		"source":        source,
		"mode":               mode,
		repoAnalysisKeyBaseRef: strings.TrimSpace(request.BaseRef),
		"path":          path,
		"command":       command,
		"exit_code":     exitCode,
		"changed_count": len(changed),
		"changed_files": changed,
		"summary":       summary,
		"output":        summary,
	}

	artifacts := []app.ArtifactPayload{}
	if reportBytes, marshalErr := json.MarshalIndent(resultOutput, "", "  "); marshalErr == nil {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "changed-files-summary.json",
			ContentType: repoAnalysisContentTypeJSON,
			Data:        reportBytes,
		})
	}

	return app.ToolRunResult{
		ExitCode:  exitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: repoAnalysisKeyStdout, Message: summary}},
		Output:    resultOutput,
		Artifacts: artifacts,
	}, nil
}

func (h *RepoSymbolSearchHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Symbol        string `json:"symbol"`
		Path          string `json:"path"`
		MaxResults    int    `json:"max_results"`
		CaseSensitive bool   `json:"case_sensitive"`
		WholeWord     bool   `json:"whole_word"`
		UseRegex      bool   `json:"use_regex"`
	}{
		Path:          ".",
		MaxResults:    200,
		CaseSensitive: true,
		WholeWord:     false,
		UseRegex:      false,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.symbol_search args",
				Retryable: false,
			}
		}
	}

	symbol := strings.TrimSpace(request.Symbol)
	if symbol == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "symbol is required",
			Retryable: false,
		}
	}
	if len(symbol) > 160 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "symbol exceeds 160 characters",
			Retryable: false,
		}
	}
	if request.UseRegex {
		if _, compileErr := regexp.Compile(symbol); compileErr != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   compileErr.Error(),
				Retryable: false,
			}
		}
	}

	path, pathErr := sanitizeRelativePath(request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   pathErr.Error(),
			Retryable: false,
		}
	}
	if path == "" {
		path = "."
	}
	maxResults := clampInt(request.MaxResults, 1, 2000, 200)

	searchRun, runErr := runRepoSymbolSearch(ctx, ensureRunner(h.runner), session, repoSymbolSearchSpec{
		Symbol:        symbol,
		Path:          path,
		MaxResults:    maxResults,
		CaseSensitive: request.CaseSensitive,
		WholeWord:     request.WholeWord,
		UseRegex:      request.UseRegex,
	})
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	summary := fmt.Sprintf("found %d symbol matches", len(searchRun.Matches))
	resultOutput := map[string]any{
		"symbol":         symbol,
		"path":                    path,
		repoAnalysisKeyCaseSensitive: request.CaseSensitive,
		"whole_word":     request.WholeWord,
		"use_regex":      request.UseRegex,
		"command":        searchRun.Command,
		"exit_code":      searchRun.ExitCode,
		"matches_count":  len(searchRun.Matches),
		"truncated":      searchRun.Truncated,
		"matches":        searchRun.Matches,
		"summary":        summary,
		"output":         summary,
	}

	artifacts := []app.ArtifactPayload{}
	if reportBytes, marshalErr := json.MarshalIndent(resultOutput, "", "  "); marshalErr == nil {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "symbol-search-results.json",
			ContentType: repoAnalysisContentTypeJSON,
			Data:        reportBytes,
		})
	}

	return app.ToolRunResult{
		ExitCode:  searchRun.ExitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: repoAnalysisKeyStdout, Message: summary}},
		Output:    resultOutput,
		Artifacts: artifacts,
	}, nil
}

func (h *RepoFindReferencesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Symbol              string `json:"symbol"`
		Path                string `json:"path"`
		MaxReferences       int    `json:"max_references"`
		MaxResults          int    `json:"max_results"`
		CaseSensitive       bool   `json:"case_sensitive"`
		IncludeDeclarations bool   `json:"include_declarations"`
	}{
		Path:                ".",
		MaxReferences:       200,
		CaseSensitive:       true,
		IncludeDeclarations: true,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.find_references args",
				Retryable: false,
			}
		}
	}

	if request.MaxReferences <= 0 && request.MaxResults > 0 {
		request.MaxReferences = request.MaxResults
	}
	maxReferences := clampInt(request.MaxReferences, 1, 2000, 200)

	symbol := strings.TrimSpace(request.Symbol)
	if symbol == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "symbol is required",
			Retryable: false,
		}
	}
	if len(symbol) > 160 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "symbol exceeds 160 characters",
			Retryable: false,
		}
	}

	path, pathErr := sanitizeRelativePath(request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   pathErr.Error(),
			Retryable: false,
		}
	}
	if path == "" {
		path = "."
	}

	searchRun, runErr := runRepoSymbolSearch(ctx, ensureRunner(h.runner), session, repoSymbolSearchSpec{
		Symbol:        symbol,
		Path:          path,
		MaxResults:    maxReferences,
		CaseSensitive: request.CaseSensitive,
		WholeWord:     true,
		UseRegex:      false,
	})
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	references, declarationCount := collectReferences(searchRun.Matches, symbol, request.CaseSensitive, request.IncludeDeclarations, maxReferences)

	truncated := searchRun.Truncated
	if len(references) >= maxReferences && len(searchRun.Matches) >= maxReferences {
		truncated = true
	}
	summary := fmt.Sprintf("found %d symbol references", len(references))
	resultOutput := map[string]any{
		"symbol":               symbol,
		"path":                        path,
		repoAnalysisKeyCaseSensitive: request.CaseSensitive,
		"include_declarations": request.IncludeDeclarations,
		"command":              searchRun.Command,
		"exit_code":            searchRun.ExitCode,
		"references_count":     len(references),
		"declaration_count":    declarationCount,
		"truncated":            truncated,
		"references":           references,
		"summary":              summary,
		"output":               summary,
	}

	artifacts := []app.ArtifactPayload{}
	if reportBytes, marshalErr := json.MarshalIndent(resultOutput, "", "  "); marshalErr == nil {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "references-summary.json",
			ContentType: repoAnalysisContentTypeJSON,
			Data:        reportBytes,
		})
	}

	return app.ToolRunResult{
		ExitCode:  searchRun.ExitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: repoAnalysisKeyStdout, Message: summary}},
		Output:    resultOutput,
		Artifacts: artifacts,
	}, nil
}

func collectReferences(matches []repoSymbolMatch, symbol string, caseSensitive, includeDeclarations bool, max int) ([]repoReferenceMatch, int) {
	references := make([]repoReferenceMatch, 0, len(matches))
	declarationCount := 0
	for _, match := range matches {
		isDeclaration := looksLikeSymbolDeclaration(match.Snippet, symbol, caseSensitive)
		if isDeclaration {
			declarationCount++
		}
		if !includeDeclarations && isDeclaration {
			continue
		}
		references = append(references, repoReferenceMatch{
			Path:          match.Path,
			Line:          match.Line,
			Column:        match.Column,
			Snippet:       match.Snippet,
			IsDeclaration: isDeclaration,
		})
		if len(references) >= max {
			break
		}
	}
	return references, declarationCount
}

type repoAnalysisRun struct {
	ProjectType projectType
	Command     []string
	Result      app.CommandResult
}

type repoSymbolSearchSpec struct {
	Symbol        string
	Path          string
	MaxResults    int
	CaseSensitive bool
	WholeWord     bool
	UseRegex      bool
}

type repoSymbolSearchRun struct {
	Command   []string
	ExitCode  int
	Matches   []repoSymbolMatch
	Truncated bool
}

func runRepoTestsForAnalysis(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	target string,
	extraArgs []string,
) (repoAnalysisRun, *domain.Error) {
	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		return repoAnalysisRun{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	safeExtraArgs, extraErr := filterRepoExtraArgs(detected, extraArgs, "test")
	if extraErr != nil {
		return repoAnalysisRun{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   extraErr.Error(),
			Retryable: false,
		}
	}

	command, commandArgs, commandErr := testCommandForProject(session.WorkspacePath, detected, target, safeExtraArgs)
	if commandErr != nil {
		return repoAnalysisRun{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   commandErr.Error(),
			Retryable: false,
		}
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  command,
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})
	if runErr != nil && (strings.Contains(strings.ToLower(runErr.Error()), "timeout") || commandResult.ExitCode == 124) {
		return repoAnalysisRun{}, &domain.Error{
			Code:      app.ErrorCodeTimeout,
			Message:   "test command timed out",
			Retryable: true,
		}
	}

	return repoAnalysisRun{
		ProjectType: detected,
		Command:     append([]string{command}, commandArgs...),
		Result:      commandResult,
	}, nil
}

func runRepoSymbolSearch(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	spec repoSymbolSearchSpec,
) (repoSymbolSearchRun, *domain.Error) {
	resolvedPath, pathErr := resolvePath(session, spec.Path)
	if pathErr != nil {
		return repoSymbolSearchRun{}, pathErr
	}

	commandArgs := []string{"-R", "-n", "-H", "--binary-files=without-match"}
	if !spec.CaseSensitive {
		commandArgs = append(commandArgs, "-i")
	}
	if spec.WholeWord {
		commandArgs = append(commandArgs, "-w")
	}
	if spec.UseRegex {
		commandArgs = append(commandArgs, "-E")
	} else {
		commandArgs = append(commandArgs, "-F")
	}
	commandArgs = append(commandArgs, "--", spec.Symbol, resolvedPath)

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "grep",
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})
	if runErr != nil && commandResult.ExitCode != 1 {
		return repoSymbolSearchRun{}, toToolError(runErr, commandResult.Output)
	}

	matches := parseRepoSearchMatches(
		commandResult.Output,
		session.WorkspacePath,
		spec.Symbol,
		spec.UseRegex,
		spec.CaseSensitive,
	)
	truncated := false
	if len(matches) > spec.MaxResults {
		matches = matches[:spec.MaxResults]
		truncated = true
	}

	return repoSymbolSearchRun{
		Command:   append([]string{"grep"}, commandArgs...),
		ExitCode:  commandResult.ExitCode,
		Matches:   matches,
		Truncated: truncated,
	}, nil
}

func summarizeChangedFiles(output, mode string, includeUntracked bool, maxFiles int) []repoChangedFile {
	rawLines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	files := make([]repoChangedFile, 0, minInt(len(rawLines), maxFiles))

	resolvedMode := resolveChangedFilesMode(mode, rawLines)
	if resolvedMode == repoAnalysisKeyWorkingTree {
		files = collectWorkingTreeFiles(rawLines, includeUntracked, maxFiles)
	} else {
		files = collectDiffFiles(rawLines, includeUntracked, maxFiles)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Path == files[j].Path {
			return files[i].Status < files[j].Status
		}
		return files[i].Path < files[j].Path
	})
	if len(files) > maxFiles {
		return files[:maxFiles]
	}
	return files
}

func resolveChangedFilesMode(mode string, rawLines []string) string {
	if mode == repoAnalysisKeyWorkingTree || mode == repoAnalysisKeyBaseRefDiff {
		return mode
	}
	for _, raw := range rawLines {
		if strings.HasPrefix(raw, "?? ") || (len(raw) >= 3 && raw[2] == ' ') {
			return repoAnalysisKeyWorkingTree
		}
	}
	return repoAnalysisKeyBaseRefDiff
}

func collectWorkingTreeFiles(rawLines []string, includeUntracked bool, maxFiles int) []repoChangedFile {
	files := make([]repoChangedFile, 0, minInt(len(rawLines), maxFiles))
	for _, raw := range rawLines {
		if len(files) >= maxFiles {
			break
		}
		entry, ok := parseGitStatusPorcelainLine(raw, includeUntracked)
		if !ok || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		files = append(files, entry)
	}
	return files
}

func collectDiffFiles(rawLines []string, includeUntracked bool, maxFiles int) []repoChangedFile {
	files := make([]repoChangedFile, 0, minInt(len(rawLines), maxFiles))
	for _, raw := range rawLines {
		if len(files) >= maxFiles {
			break
		}
		entry, ok := parseGitNameStatusLine(raw)
		if !ok || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		if entry.Untracked && !includeUntracked {
			continue
		}
		files = append(files, entry)
	}
	return files
}

func parseGitStatusPorcelainLine(line string, includeUntracked bool) (repoChangedFile, bool) {
	if strings.TrimSpace(line) == "" || len(line) < 3 {
		return repoChangedFile{}, false
	}
	indexStatus := line[0]
	worktreeStatus := line[1]
	pathPart := strings.TrimSpace(line[3:])
	if pathPart == "" {
		return repoChangedFile{}, false
	}

	path := strings.Trim(pathPart, "\"")
	renamedFrom := ""
	if strings.Contains(pathPart, " -> ") {
		parts := strings.SplitN(pathPart, " -> ", 2)
		if len(parts) == 2 {
			renamedFrom = strings.TrimSpace(strings.Trim(parts[0], "\""))
			path = strings.TrimSpace(strings.Trim(parts[1], "\""))
		}
	}

	untracked := indexStatus == '?' && worktreeStatus == '?'
	if untracked && !includeUntracked {
		return repoChangedFile{}, false
	}

	return repoChangedFile{
		Path:           filepath.ToSlash(path),
		Status:         normalizeGitStatus(indexStatus, worktreeStatus, untracked),
		IndexStatus:    string(indexStatus),
		WorktreeStatus: string(worktreeStatus),
		Staged:         indexStatus != ' ' && indexStatus != '?',
		Unstaged:       worktreeStatus != ' ',
		Untracked:      untracked,
		Deleted:        indexStatus == 'D' || worktreeStatus == 'D',
		RenamedFrom:    filepath.ToSlash(renamedFrom),
	}, true
}

func parseGitNameStatusLine(line string) (repoChangedFile, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return repoChangedFile{}, false
	}

	parts := strings.Split(trimmed, "\t")
	if len(parts) < 2 {
		return repoChangedFile{}, false
	}
	statusToken := strings.TrimSpace(parts[0])
	if statusToken == "" {
		return repoChangedFile{}, false
	}

	code := statusToken[0]
	path := strings.TrimSpace(parts[1])
	renamedFrom := ""
	if code == 'R' && len(parts) >= 3 {
		renamedFrom = path
		path = strings.TrimSpace(parts[2])
	}
	if path == "" {
		return repoChangedFile{}, false
	}

	untracked := code == '?'
	return repoChangedFile{
		Path:           filepath.ToSlash(path),
		Status:         normalizeGitStatus(code, ' ', untracked),
		Staged:         false,
		Unstaged:       false,
		Untracked:      untracked,
		Deleted:        code == 'D',
		RenamedFrom:    filepath.ToSlash(renamedFrom),
		IndexStatus:    string(code),
		WorktreeStatus: "",
	}, true
}

func normalizeGitStatus(indexStatus byte, worktreeStatus byte, untracked bool) string {
	if untracked {
		return "untracked"
	}

	for _, code := range []byte{indexStatus, worktreeStatus} {
		switch code {
		case 'A':
			return "added"
		case 'D':
			return "deleted"
		case 'R':
			return "renamed"
		case 'C':
			return "copied"
		case 'U':
			return "conflict"
		case 'M':
			return "modified"
		}
	}
	return "changed"
}

func parseRepoSearchMatches(output, workspacePath, symbol string, useRegex bool, caseSensitive bool) []repoSymbolMatch {
	lines := splitOutputLines(output)
	matches := make([]repoSymbolMatch, 0, len(lines))
	for _, line := range lines {
		matchPath, lineNumber, snippet, ok := parseGrepLine(line)
		if !ok {
			continue
		}
		relativePath, relErr := filepath.Rel(workspacePath, matchPath)
		if relErr != nil || strings.HasPrefix(relativePath, "..") {
			relativePath = matchPath
		}
		matches = append(matches, repoSymbolMatch{
			Path:    filepath.ToSlash(relativePath),
			Line:    lineNumber,
			Column:  inferMatchColumn(snippet, symbol, useRegex, caseSensitive),
			Snippet: snippet,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path == matches[j].Path {
			if matches[i].Line == matches[j].Line {
				return matches[i].Column < matches[j].Column
			}
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Path < matches[j].Path
	})
	return matches
}

func inferMatchColumn(snippet, symbol string, useRegex bool, caseSensitive bool) int {
	if snippet == "" || symbol == "" {
		return 0
	}
	if !useRegex {
		haystack := snippet
		needle := symbol
		if !caseSensitive {
			haystack = strings.ToLower(snippet)
			needle = strings.ToLower(symbol)
		}
		index := strings.Index(haystack, needle)
		if index >= 0 {
			return index + 1
		}
		return 0
	}

	pattern := symbol
	if !caseSensitive {
		pattern = "(?i)" + symbol
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return 0
	}
	location := compiled.FindStringIndex(snippet)
	if location == nil {
		return 0
	}
	return location[0] + 1
}

func looksLikeSymbolDeclaration(snippet, symbol string, caseSensitive bool) bool {
	trimmed := strings.TrimSpace(snippet)
	if trimmed == "" || symbol == "" {
		return false
	}
	escaped := regexp.QuoteMeta(symbol)
	prefix := ""
	if !caseSensitive {
		prefix = "(?i)"
	}

	patterns := []string{
		prefix + `^func\s*(\([^)]*\)\s*)?` + escaped + `\b`,
		prefix + `^type\s+` + escaped + `\b`,
		prefix + `^(const|let|var)\s+` + escaped + `\b`,
		prefix + `^def\s+` + escaped + `\b`,
		prefix + `^class\s+` + escaped + `\b`,
		prefix + `^fn\s+` + escaped + `\b`,
		prefix + `^interface\s+` + escaped + `\b`,
		prefix + `^struct\s+` + escaped + `\b`,
		prefix + `^enum\s+` + escaped + `\b`,
		prefix + `^` + escaped + `\s*:=`,
	}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(trimmed) {
			return true
		}
	}
	return false
}

func summarizeTestFailures(output string, maxFailures int) []summarizedFailure {
	lines := splitOutputLines(output)
	failures := make([]summarizedFailure, 0, minInt(len(lines), maxFailures))
	seen := map[string]bool{}

	for _, line := range lines {
		if len(failures) >= maxFailures {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		appendTestFailureLine(trimmed, seen, &failures)
	}

	return failures
}

func appendTestFailureLine(trimmed string, seen map[string]bool, failures *[]summarizedFailure) {
	if matches := goFailPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		recordTestFailure(matches[1], "go", trimmed, seen, failures)
		return
	}
	if matches := pytestFailPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		recordTestFailure(matches[1], "python", trimmed, seen, failures)
		return
	}
	if matches := cargoFailPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		recordTestFailure(matches[1], "rust", trimmed, seen, failures)
		return
	}
	if matches := jestFailPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		recordTestFailure(matches[1], "node", trimmed, seen, failures)
		return
	}
	if matches := goPackageFailPattern.FindStringSubmatch(trimmed); len(matches) == 2 && strings.Contains(matches[1], "/") {
		recordTestFailure(matches[1], "package", trimmed, seen, failures)
	}
}

func recordTestFailure(name, kind, line string, seen map[string]bool, failures *[]summarizedFailure) {
	candidate := strings.TrimSpace(name)
	if candidate == "" || seen[kind+"|"+candidate] {
		return
	}
	seen[kind+"|"+candidate] = true
	*failures = append(*failures, summarizedFailure{
		Name: candidate,
		Kind: kind,
		Line: strings.TrimSpace(line),
	})
}

func summarizeStacktraces(output string, maxTraces int, maxFrames int) []summarizedStacktrace {
	rawLines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	traces := make([]summarizedStacktrace, 0, minInt(len(rawLines), maxTraces))

	for i := 0; i < len(rawLines) && len(traces) < maxTraces; i++ {
		line := strings.TrimSpace(rawLines[i])
		traceType, isStart := classifyStacktraceStart(line)
		if !isStart {
			continue
		}
		frames, nextI := collectStacktraceFrames(rawLines, i+1, maxFrames)
		i = nextI
		traces = append(traces, summarizedStacktrace{
			Type:       traceType,
			Message:    line,
			Frames:     frames,
			FrameCount: len(frames),
		})
	}

	return traces
}

func collectStacktraceFrames(rawLines []string, start, maxFrames int) ([]string, int) {
	frames := make([]string, 0, maxFrames)
	i := start - 1
	for j := start; j < len(rawLines); j++ {
		candidate := strings.TrimSpace(rawLines[j])
		if candidate == "" {
			if len(frames) > 0 {
				i = j
				break
			}
			continue
		}
		if _, isNext := classifyStacktraceStart(candidate); isNext {
			i = j - 1
			break
		}
		if looksLikeStacktraceFrame(candidate) {
			frames = append(frames, candidate)
			if len(frames) >= maxFrames {
				i = j
				break
			}
		}
	}
	return frames, i
}

func classifyStacktraceStart(line string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(lower, "panic:"):
		return "panic", true
	case strings.HasPrefix(lower, "fatal error:"):
		return "fatal_error", true
	case strings.HasPrefix(lower, "traceback (most recent call last):"):
		return "python_traceback", true
	case strings.HasPrefix(lower, "exception in thread"):
		return "java_exception", true
	case threadPanicPattern.MatchString(lower):
		return "rust_panic", true
	case strings.Contains(lower, "stack trace"):
		return "stack_trace", true
	default:
		return "", false
	}
}

func looksLikeStacktraceFrame(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "at ") || strings.HasPrefix(line, "#") {
		return true
	}
	if strings.HasPrefix(line, "goroutine ") || strings.HasPrefix(line, "Caused by:") {
		return true
	}
	if pythonFramePattern.MatchString(line) || goFramePattern.MatchString(line) {
		return true
	}
	if strings.Contains(line, ".go:") || strings.Contains(line, ".py") || strings.Contains(line, ".rs:") || strings.Contains(line, ".java:") {
		return true
	}
	return false
}
