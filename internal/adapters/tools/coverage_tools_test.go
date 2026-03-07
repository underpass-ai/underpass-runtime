package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestRepoCoverageReportHandler_Go(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				return app.CommandResult{ExitCode: 0, Output: "ok\tcoverage: 71.4% of statements"}, nil
			case 1:
				return app.CommandResult{ExitCode: 0, Output: "total:\t(statements)\t80.0%"}, nil
			case 2:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			default:
				t.Fatalf("unexpected call index %d", callIndex)
				return app.CommandResult{}, nil
			}
		},
	}

	handler := NewRepoCoverageReportHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected repo.coverage_report error: %#v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 runner calls, got %d", len(runner.calls))
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	if output["project_type"] != "go" {
		t.Fatalf("unexpected project_type: %#v", output["project_type"])
	}
	coverage, ok := output["coverage_percent"].(float64)
	if !ok || coverage != 80.0 {
		t.Fatalf("unexpected coverage_percent: %#v", output["coverage_percent"])
	}
}

func TestRepoCoverageReportHandler_GoCoverCommandFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				return app.CommandResult{ExitCode: 0, Output: "ok\tcoverage: 55.0% of statements"}, nil
			case 1:
				if spec.Command != "go" {
					t.Fatalf("unexpected cover command: %q", spec.Command)
				}
				return app.CommandResult{ExitCode: 1, Output: "cover failed"}, errors.New("exit 1")
			case 2:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			default:
				t.Fatalf("unexpected call index %d", callIndex)
				return app.CommandResult{}, nil
			}
		},
	}

	result, err := NewRepoCoverageReportHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected cover command execution failure, got %#v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1 from cover failure, got %d", result.ExitCode)
	}
}

func TestRepoCoverageReportHandler_NonGoPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "npm" {
				t.Fatalf("expected npm command for non-go coverage, got %q", spec.Command)
			}
			return app.CommandResult{ExitCode: 0, Output: "tests passed"}, nil
		},
	}
	result, err := NewRepoCoverageReportHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected non-go coverage error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["project_type"] != "node" {
		t.Fatalf("unexpected project_type: %#v", output["project_type"])
	}
	if output["coverage_supported"] != false {
		t.Fatalf("expected coverage_supported=false, got %#v", output["coverage_supported"])
	}
}

func TestRepoCoverageReportHandler_InvalidArgs(t *testing.T) {
	handler := NewRepoCoverageReportHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestRepoCoverageReportHandler_NoToolchain(t *testing.T) {
	handler := NewRepoCoverageReportHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected no toolchain error, got %#v", err)
	}
}

func TestRepoCoverageReportHandler_GoTestFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			if callIndex == 0 {
				return app.CommandResult{ExitCode: 1, Output: "test failed"}, errors.New("exit 1")
			}
			// rm cleanup
			return app.CommandResult{ExitCode: 0, Output: ""}, nil
		},
	}
	result, err := NewRepoCoverageReportHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error on test failure")
	}
	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}
}
