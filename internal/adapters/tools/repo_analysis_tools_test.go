package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeRepoAnalysisRunner struct {
	result app.CommandResult
	err    error
}

func (f *fakeRepoAnalysisRunner) Run(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
	return f.result, f.err
}

func TestRepoTestFailuresSummaryHandler_WithProvidedOutput(t *testing.T) {
	handler := NewRepoTestFailuresSummaryHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	output := strings.Join([]string{
		"--- FAIL: TestCreateTodo (0.00s)",
		"FAILED tests/test_todo.py::test_add_todo - AssertionError: expected 1 got 0",
		"test service::tests::it_updates ... FAILED",
	}, "\n")

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"output":`+mustJSONQuote(output)+`}`))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	parsed := result.Output.(map[string]any)
	if parsed["failed_count"] != 3 {
		t.Fatalf("expected failed_count=3, got %#v", parsed["failed_count"])
	}
	if parsed["source"] != "provided_output" {
		t.Fatalf("unexpected source: %#v", parsed["source"])
	}
	failures, ok := parsed["failed_tests"].([]summarizedFailure)
	if !ok {
		t.Fatalf("unexpected failed_tests type: %T", parsed["failed_tests"])
	}
	if len(failures) != 3 {
		t.Fatalf("expected 3 failures, got %d", len(failures))
	}
}

func TestRepoStacktraceSummaryHandler_WithProvidedOutput(t *testing.T) {
	handler := NewRepoStacktraceSummaryHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	output := strings.Join([]string{
		"panic: runtime error: index out of range",
		"goroutine 1 [running]:",
		"main.main()",
		"/workspace/repo/main.go:11 +0x29",
		"",
		"Traceback (most recent call last):",
		"File \"/workspace/repo/test_app.py\", line 10, in test_fail",
		"assert 1 == 2",
	}, "\n")

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"output":`+mustJSONQuote(output)+`}`))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	parsed := result.Output.(map[string]any)
	if parsed["trace_count"] != 2 {
		t.Fatalf("expected trace_count=2, got %#v", parsed["trace_count"])
	}
	traces, ok := parsed["stacktraces"].([]summarizedStacktrace)
	if !ok {
		t.Fatalf("unexpected stacktraces type: %T", parsed["stacktraces"])
	}
	if len(traces) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(traces))
	}
	if traces[0].Type != "panic" {
		t.Fatalf("unexpected first trace type: %s", traces[0].Type)
	}
}

func TestRepoTestFailuresSummaryHandler_RunsTestsWhenOutputMissing(t *testing.T) {
	runner := &fakeRepoAnalysisRunner{
		result: app.CommandResult{
			Output:   "--- FAIL: TestListTodos (0.00s)\nFAIL\tgithub.com/acme/todo\t0.01s\n",
			ExitCode: 1,
		},
		err: errors.New("exit status 1"),
	}
	handler := NewRepoTestFailuresSummaryHandler(runner)
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
	}
	// force go detection by writing go.mod
	if writeErr := os.WriteFile(session.WorkspacePath+"/go.mod", []byte("module demo\n\ngo 1.23\n"), 0o644); writeErr != nil {
		t.Fatalf("write go.mod: %v", writeErr)
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected success even with failed tests, got %#v", err)
	}
	parsed := result.Output.(map[string]any)
	if parsed["source"] != "test_run" {
		t.Fatalf("unexpected source: %#v", parsed["source"])
	}
	if parsed["exit_code"] != 1 {
		t.Fatalf("unexpected exit_code: %#v", parsed["exit_code"])
	}
}

func TestRepoChangedFilesHandler_WithProvidedOutput(t *testing.T) {
	handler := NewRepoChangedFilesHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	output := strings.Join([]string{
		" M internal/app/service.go",
		"A  internal/app/new_file.go",
		"R  internal/app/old_name.go -> internal/app/new_name.go",
		"?? scratch.txt",
	}, "\n")

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"output":`+mustJSONQuote(output)+`,"include_untracked":false}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	parsed := result.Output.(map[string]any)
	if parsed["changed_count"] != 3 {
		t.Fatalf("expected changed_count=3, got %#v", parsed["changed_count"])
	}
	changed, ok := parsed["changed_files"].([]repoChangedFile)
	if !ok {
		t.Fatalf("unexpected changed_files type: %T", parsed["changed_files"])
	}
	if len(changed) != 3 {
		t.Fatalf("expected 3 changed files, got %d", len(changed))
	}
	renamedFound := false
	for _, file := range changed {
		if file.Status == "renamed" && file.RenamedFrom == "internal/app/old_name.go" {
			renamedFound = true
			break
		}
	}
	if !renamedFound {
		t.Fatalf("expected renamed entry, got %#v", changed)
	}
}

func TestRepoSymbolSearchHandler_WithRunnerOutput(t *testing.T) {
	runner := &fakeRepoAnalysisRunner{
		result: app.CommandResult{
			Output: strings.Join([]string{
				"/workspace/repo/internal/todo.go:12:func CreateTodo() error {",
				"/workspace/repo/internal/todo_test.go:35:CreateTodo()",
			}, "\n"),
			ExitCode: 0,
		},
	}
	handler := NewRepoSymbolSearchHandler(runner)
	session := domain.Session{WorkspacePath: "/workspace/repo", AllowedPaths: []string{"."}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"symbol":"CreateTodo","path":".","max_results":10}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	parsed := result.Output.(map[string]any)
	if parsed["matches_count"] != 2 {
		t.Fatalf("expected matches_count=2, got %#v", parsed["matches_count"])
	}
	matches, ok := parsed["matches"].([]repoSymbolMatch)
	if !ok {
		t.Fatalf("unexpected matches type: %T", parsed["matches"])
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Path != "internal/todo.go" {
		t.Fatalf("unexpected first match path: %#v", matches[0].Path)
	}
	if matches[0].Column <= 0 {
		t.Fatalf("expected positive column, got %d", matches[0].Column)
	}
}

func TestRepoFindReferencesHandler_ExcludesDeclarations(t *testing.T) {
	runner := &fakeRepoAnalysisRunner{
		result: app.CommandResult{
			Output: strings.Join([]string{
				"/workspace/repo/pkg/todo.go:5:func CreateTodo() error {",
				"/workspace/repo/pkg/service.go:22:return CreateTodo()",
			}, "\n"),
			ExitCode: 0,
		},
	}
	handler := NewRepoFindReferencesHandler(runner)
	session := domain.Session{WorkspacePath: "/workspace/repo", AllowedPaths: []string{"."}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"symbol":"CreateTodo","include_declarations":false}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	parsed := result.Output.(map[string]any)
	if parsed["references_count"] != 1 {
		t.Fatalf("expected references_count=1, got %#v", parsed["references_count"])
	}
	if parsed["declaration_count"] != 1 {
		t.Fatalf("expected declaration_count=1, got %#v", parsed["declaration_count"])
	}
	references, ok := parsed["references"].([]repoReferenceMatch)
	if !ok {
		t.Fatalf("unexpected references type: %T", parsed["references"])
	}
	if len(references) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(references))
	}
	if references[0].IsDeclaration {
		t.Fatalf("expected non-declaration reference, got %#v", references[0])
	}
}

func TestRepoAnalysisHandlerNames(t *testing.T) {
	if NewRepoTestFailuresSummaryHandler(nil).Name() != "repo.test_failures_summary" {
		t.Fatal("unexpected repo.test_failures_summary name")
	}
	if NewRepoStacktraceSummaryHandler(nil).Name() != "repo.stacktrace_summary" {
		t.Fatal("unexpected repo.stacktrace_summary name")
	}
	if NewRepoChangedFilesHandler(nil).Name() != "repo.changed_files" {
		t.Fatal("unexpected repo.changed_files name")
	}
	if NewRepoSymbolSearchHandler(nil).Name() != "repo.symbol_search" {
		t.Fatal("unexpected repo.symbol_search name")
	}
	if NewRepoFindReferencesHandler(nil).Name() != "repo.find_references" {
		t.Fatal("unexpected repo.find_references name")
	}
}

func TestRepoChangedFilesHandler_RunsGitWhenOutputMissing(t *testing.T) {
	runner := &fakeRepoAnalysisRunner{
		result: app.CommandResult{
			Output: strings.Join([]string{
				"M\tinternal/app/service.go",
				"A\tinternal/app/new_file.go",
				"R100\told.go\tnew.go",
			}, "\n"),
			ExitCode: 0,
		},
	}
	handler := NewRepoChangedFilesHandler(runner)
	session := domain.Session{WorkspacePath: "/workspace/repo", AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected repo.changed_files git run error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["source"] != "git" {
		t.Fatalf("unexpected source: %#v", output["source"])
	}
	if output["changed_count"] != 3 {
		t.Fatalf("unexpected changed_count: %#v", output["changed_count"])
	}
}

func TestRepoAnalysisParsingHelpers(t *testing.T) {
	rename, ok := parseGitNameStatusLine("R100\told.go\tnew.go")
	if !ok || rename.Path != "new.go" || rename.Status != "renamed" || rename.RenamedFrom != "old.go" {
		t.Fatalf("unexpected parseGitNameStatusLine rename result: %#v ok=%v", rename, ok)
	}
	add, ok := parseGitNameStatusLine("A\tnew.go")
	if !ok || add.Path != "new.go" || add.Status != "added" || add.RenamedFrom != "" {
		t.Fatalf("unexpected parseGitNameStatusLine add result: %#v ok=%v", add, ok)
	}
	if _, ok := parseGitNameStatusLine("invalid line"); ok {
		t.Fatal("expected parseGitNameStatusLine failure on invalid line")
	}

	if normalizeGitStatus('?', '?', true) != "untracked" {
		t.Fatal("expected normalizeGitStatus for ??")
	}
	if normalizeGitStatus('R', ' ', false) != "renamed" {
		t.Fatal("expected normalizeGitStatus for R")
	}
	if normalizeGitStatus('X', 'Y', false) != "changed" {
		t.Fatal("expected normalizeGitStatus fallback changed")
	}

	matches := parseRepoSearchMatches(
		"/workspace/repo/internal/todo.go:10:func CreateTodo() {}\n"+
			"/workspace/repo/internal/service.go:20:return CreateTodo()\n",
		"/workspace/repo",
		"CreateTodo",
		false,
		true,
	)
	if len(matches) != 2 {
		t.Fatalf("expected 2 parsed repo search matches, got %d", len(matches))
	}
	if matches[0].Column <= 0 {
		t.Fatalf("expected inferred column > 0, got %d", matches[0].Column)
	}
	if inferMatchColumn("no symbol here", "CreateTodo", false, true) != 0 {
		t.Fatalf("expected inferMatchColumn fallback column 0")
	}

	traceType, found := classifyStacktraceStart("panic: runtime error")
	if !found || traceType != "panic" {
		t.Fatal("expected panic stacktrace classification")
	}
	if !looksLikeStacktraceFrame("at foo.bar(Foo.java:10)") {
		t.Fatal("expected java-like stacktrace frame detection")
	}
}

func TestRunRepoHelpersWithMissingRunner(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	errorRunner := &fakeRepoAnalysisRunner{
		result: app.CommandResult{ExitCode: 1, Output: "runner failed"},
		err:    errors.New("runner failed"),
	}

	if _, err := runRepoTestsForAnalysis(context.Background(), errorRunner, session, "./...", nil); err == nil {
		t.Fatal("expected runRepoTestsForAnalysis to fail without runner")
	}
	if _, err := runRepoSymbolSearch(context.Background(), errorRunner, session, repoSymbolSearchSpec{
		Path:          "../outside",
		Symbol:        "CreateTodo",
		MaxResults:    10,
		UseRegex:      false,
		CaseSensitive: true,
	}); err == nil {
		t.Fatal("expected runRepoSymbolSearch to fail for invalid path")
	}
}

func mustJSONQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
