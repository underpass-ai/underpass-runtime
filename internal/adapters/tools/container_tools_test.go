package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testContainerRuntimePodman     = "podman"
	testContainerRuntimeDocker     = "docker"
	testContainerSessionID1        = "sess1"
	testContainerImageNginxLatest  = "nginx:latest"
	testContainerImageBusyboxLatest = "busybox:latest"
	testContainerNameMycontainer   = "mycontainer"
	testContainerCmdNginx          = "nginx"
	testContainerArgInfo           = "info"
	testContainerErrExitStatus1    = "exit status 1"
	testContainerErrUnexpectedCmd  = "unexpected command"
	testContainerErrCannotConnect  = "cannot connect to runtime"
	testContainerFmtExecFailed     = "expected execution_failed, got %s"
	testContainerFmtPodmanRuntime  = "expected podman runtime output, got %#v"
)

type fakeContainerRunner struct {
	calls []app.CommandSpec
	run   func(callIndex int, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeContainerRunner) Run(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if f.run != nil {
		return f.run(len(f.calls)-1, spec)
	}
	return app.CommandResult{ExitCode: 0}, nil
}

func TestContainerPSHandler_SimulatedWhenRuntimeUnavailable(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 1, Output: testContainerErrCannotConnect}, errors.New(testContainerErrExitStatus1)
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerPSHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":25,"strict":false}`))
	if err != nil {
		t.Fatalf("unexpected container.ps error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["simulated"] != true || output["runtime"] != "synthetic" {
		t.Fatalf("expected simulated synthetic output, got %#v", output)
	}
	if output["count"] != 0 {
		t.Fatalf("expected count=0, got %#v", output["count"])
	}
}

func TestContainerPSHandler_RuntimeAndTruncation(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == testContainerRuntimePodman && len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 0, Output: "{}"}, nil
			}
			if spec.Command == testContainerRuntimePodman && len(spec.Args) > 0 && spec.Args[0] == "ps" {
				return app.CommandResult{ExitCode: 0, Output: "b123\timg-b\tb\trunning\na123\timg-a\ta\texited"}, nil
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerPSHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatalf("unexpected container.ps error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != testContainerRuntimePodman || output["simulated"] != false {
		t.Fatalf(testContainerFmtPodmanRuntime, output)
	}
	if output["count"] != 1 || output["truncated"] != true {
		t.Fatalf("expected count=1 truncated=true, got %#v", output)
	}
	containers := output["containers"].([]map[string]any)
	if len(containers) != 1 || containers[0]["id"] != "a123" {
		t.Fatalf("expected sorted/truncated containers, got %#v", containers)
	}
}

func TestContainerRunHandler_StrictNoRuntimeFails(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 1, Output: "no runtime"}, errors.New(testContainerErrExitStatus1)
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerRunHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"image_ref":"busybox:1.36","strict":true}`))
	if err == nil {
		t.Fatal("expected strict runtime failure")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf(testContainerFmtExecFailed, err.Code)
	}
}

func TestContainerRunHandler_UsesRuntime(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == testContainerRuntimePodman && len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 0, Output: "{}"}, nil
			}
			if spec.Command == testContainerRuntimePodman && len(spec.Args) > 0 && spec.Args[0] == "run" {
				if !containsArg(spec.Args, "-d") {
					t.Fatalf("expected detach flag in run args: %#v", spec.Args)
				}
				return app.CommandResult{ExitCode: 0, Output: "abc123def456\n"}, nil
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerRunHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir(), ID: "s1"}, json.RawMessage(`{"image_ref":"busybox:1.36","command":["echo","ok"]}`))
	if err != nil {
		t.Fatalf("unexpected container.run error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != testContainerRuntimePodman || output["simulated"] != false {
		t.Fatalf(testContainerFmtPodmanRuntime, output)
	}
	if output["container_id"] != "abc123def456" {
		t.Fatalf("unexpected container_id: %#v", output["container_id"])
	}
}

func TestContainerLogsHandler_SimulatedID(t *testing.T) {
	handler := NewContainerLogsHandler(nil)
	result, err := handler.Invoke(context.Background(), domain.Session{}, json.RawMessage(`{"container_id":"sim-123456","strict":false}`))
	if err != nil {
		t.Fatalf("unexpected container.logs error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["simulated"] != true {
		t.Fatalf("expected simulated logs output, got %#v", output)
	}
	if !strings.Contains(output["logs"].(string), "simulated logs") {
		t.Fatalf("expected simulated logs text, got %#v", output["logs"])
	}
}

func TestContainerExecHandler_DeniesDisallowedCommand(t *testing.T) {
	handler := NewContainerExecHandler(nil)
	_, err := handler.Invoke(context.Background(), domain.Session{}, json.RawMessage(`{"container_id":"sim-123456","command":["rm","-rf","/"]}`))
	if err == nil {
		t.Fatal("expected command denial")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}

func TestContainerExecHandler_UsesRuntime(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == testContainerRuntimePodman && len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 0, Output: "{}"}, nil
			}
			if spec.Command == testContainerRuntimePodman && len(spec.Args) > 0 && spec.Args[0] == "exec" {
				return app.CommandResult{ExitCode: 0, Output: "hello from container"}, nil
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerExecHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"container_id":"abc123","command":["echo","hello"],"strict":true}`))
	if err != nil {
		t.Fatalf("unexpected container.exec error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != testContainerRuntimePodman || output["simulated"] != false {
		t.Fatalf(testContainerFmtPodmanRuntime, output)
	}
	if !strings.Contains(output["output"].(string), "hello") {
		t.Fatalf("unexpected exec output: %#v", output["output"])
	}
}

func TestContainerPSHandler_StrictByDefaultEnvFailsWithoutRuntime(t *testing.T) {
	t.Setenv("WORKSPACE_CONTAINER_STRICT_BY_DEFAULT", "true")
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 1, Output: testContainerErrCannotConnect}, errors.New(testContainerErrExitStatus1)
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerPSHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":25}`))
	if err == nil {
		t.Fatal("expected strict-by-default runtime failure")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf(testContainerFmtExecFailed, err.Code)
	}
}

func TestContainerPSHandler_SyntheticFallbackDisabledEnvForcesStrict(t *testing.T) {
	t.Setenv("WORKSPACE_CONTAINER_ALLOW_SYNTHETIC_FALLBACK", "false")
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 1, Output: testContainerErrCannotConnect}, errors.New(testContainerErrExitStatus1)
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerPSHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":25,"strict":false}`))
	if err == nil {
		t.Fatal("expected runtime failure when synthetic fallback disabled")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf(testContainerFmtExecFailed, err.Code)
	}
}

func TestContainerLogsHandler_SyntheticFallbackDisabledEnvForcesStrict(t *testing.T) {
	t.Setenv("WORKSPACE_CONTAINER_ALLOW_SYNTHETIC_FALLBACK", "false")
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == testContainerArgInfo {
				return app.CommandResult{ExitCode: 1, Output: testContainerErrCannotConnect}, errors.New(testContainerErrExitStatus1)
			}
			return app.CommandResult{ExitCode: 1}, errors.New(testContainerErrUnexpectedCmd)
		},
	}

	handler := NewContainerLogsHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"container_id":"sim-123456","strict":false}`))
	if err == nil {
		t.Fatal("expected runtime failure when synthetic fallback disabled")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf(testContainerFmtExecFailed, err.Code)
	}
}

func TestContainerExecHandler_DeniesShellCommands(t *testing.T) {
	handler := NewContainerExecHandler(nil)
	_, err := handler.Invoke(context.Background(), domain.Session{}, json.RawMessage(`{"container_id":"sim-123456","command":["sh","-c","echo hello"]}`))
	if err == nil {
		t.Fatal("expected shell command denial")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}

func TestContainerHandlerNames(t *testing.T) {
	if NewContainerPSHandler(nil).Name() != "container.ps" {
		t.Fatal("unexpected container.ps name")
	}
	if NewContainerLogsHandler(nil).Name() != "container.logs" {
		t.Fatal("unexpected container.logs name")
	}
	if NewContainerRunHandler(nil).Name() != "container.run" {
		t.Fatal("unexpected container.run name")
	}
	if NewContainerExecHandler(nil).Name() != "container.exec" {
		t.Fatal("unexpected container.exec name")
	}
}

func TestBuildSimulatedContainerID(t *testing.T) {
	id1 := buildSimulatedContainerID(testContainerSessionID1, testContainerImageBusyboxLatest, []string{"echo", "hi"}, testContainerNameMycontainer)
	id2 := buildSimulatedContainerID(testContainerSessionID1, testContainerImageBusyboxLatest, []string{"echo", "hi"}, testContainerNameMycontainer)
	id3 := buildSimulatedContainerID("sess2", testContainerImageBusyboxLatest, []string{"echo", "hi"}, testContainerNameMycontainer)

	if id1 != id2 {
		t.Fatalf("expected same inputs to produce same ID: %q != %q", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("expected different inputs to produce different IDs, both got %q", id1)
	}
	if !strings.HasPrefix(id1, "sim-") {
		t.Fatalf("expected ID to start with 'sim-', got %q", id1)
	}
	// result is "sim-" + 12 hex chars = 16 chars total
	if len(id1) != 16 {
		t.Fatalf("expected ID length 16, got %d: %q", len(id1), id1)
	}
}

func TestBuildContainerLogsCommand(t *testing.T) {
	// Without sinceSec and without timestamps
	cmd := buildContainerLogsCommand(testContainerRuntimeDocker, "ctr-1", 50, 0, false)
	expected := []string{testContainerRuntimeDocker, "logs", "--tail", "50", "ctr-1"}
	if len(cmd) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, cmd)
	}
	for i, v := range expected {
		if cmd[i] != v {
			t.Fatalf("expected cmd[%d]=%q, got %q", i, v, cmd[i])
		}
	}

	// With sinceSec=30 and timestamps=true
	cmd2 := buildContainerLogsCommand(testContainerRuntimeDocker, "ctr-1", 50, 30, true)
	if !containsArg(cmd2, "--since") {
		t.Fatalf("expected --since in cmd: %v", cmd2)
	}
	if !containsArg(cmd2, "30s") {
		t.Fatalf("expected 30s in cmd: %v", cmd2)
	}
	if !containsArg(cmd2, "--timestamps") {
		t.Fatalf("expected --timestamps in cmd: %v", cmd2)
	}
}

func TestShouldFallbackToContainerSimulation(t *testing.T) {
	// Empty string -> false
	if shouldFallbackToContainerSimulation("") {
		t.Fatal("expected false for empty string")
	}

	// Known error pattern -> true
	if !shouldFallbackToContainerSimulation("cannot connect to the docker daemon") {
		t.Fatal("expected true for docker daemon connection error")
	}

	// Another known pattern -> true
	if !shouldFallbackToContainerSimulation("connection refused while doing something") {
		t.Fatal("expected true for connection refused output")
	}

	// Healthy runtime output -> false
	if shouldFallbackToContainerSimulation("healthy runtime output OK") {
		t.Fatal("expected false for healthy runtime output")
	}
}

func TestSanitizeContainerEnv(t *testing.T) {
	// nil/empty map -> empty slice, no error
	pairs, err := sanitizeContainerEnv(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil map: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected empty slice for nil map, got %v", pairs)
	}

	pairs, err = sanitizeContainerEnv(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error for empty map: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected empty slice for empty map, got %v", pairs)
	}

	// Valid key=value pairs -> sorted KEY=value pairs
	pairs, err = sanitizeContainerEnv(map[string]string{
		"Z_VAR": "zval",
		"A_VAR": "aval",
	})
	if err != nil {
		t.Fatalf("unexpected error for valid env: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d: %v", len(pairs), pairs)
	}
	if pairs[0] != "A_VAR=aval" {
		t.Fatalf("expected first pair to be A_VAR=aval, got %q", pairs[0])
	}
	if pairs[1] != "Z_VAR=zval" {
		t.Fatalf("expected second pair to be Z_VAR=zval, got %q", pairs[1])
	}

	// Key with invalid characters (starts with digit) -> error
	_, err = sanitizeContainerEnv(map[string]string{"123bad": "value"})
	if err == nil {
		t.Fatal("expected error for key starting with digit")
	}

	// Value with newline -> error
	_, err = sanitizeContainerEnv(map[string]string{"GOOD_KEY": "bad\nvalue"})
	if err == nil {
		t.Fatal("expected error for value with newline")
	}

	// More than 32 entries -> error
	tooMany := make(map[string]string, containerMaxRunEnvVars+1)
	for i := 0; i <= containerMaxRunEnvVars; i++ {
		tooMany[fmt.Sprintf("KEY_%d", i)] = "val"
	}
	_, err = sanitizeContainerEnv(tooMany)
	if err == nil {
		t.Fatal("expected error for too many env vars")
	}

	// Value longer than 256 bytes -> error
	longVal := strings.Repeat("x", containerMaxCommandArgLength+1)
	_, err = sanitizeContainerEnv(map[string]string{"MY_KEY": longVal})
	if err == nil {
		t.Fatal("expected error for value exceeding max length")
	}
}

func TestBuildSimulatedContainerRunResult_Detach(t *testing.T) {
	opts := simulatedContainerRunOptions{
		sessionID:     testContainerSessionID1,
		imageRef:      testContainerImageNginxLatest,
		containerName: testContainerNameMycontainer,
		command:       []string{testContainerCmdNginx},
		envPairs:      []string{"FOO=bar"},
		detach:        true,
		remove:        false,
		outputText:    "some output",
	}
	result := buildSimulatedContainerRunResult(opts)
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "running" {
		t.Fatalf("expected status='running' for detach=true, got %q", output[containerKeyStatus])
	}
	if output[containerSourceSimulated] != true {
		t.Fatal("expected simulated=true")
	}
	if output["image_ref"] != testContainerImageNginxLatest {
		t.Fatalf("expected image_ref='nginx:latest', got %q", output["image_ref"])
	}
}

func TestBuildSimulatedContainerRunResult_NonDetach(t *testing.T) {
	opts := simulatedContainerRunOptions{
		sessionID:     "sess2",
		imageRef:      "alpine:3",
		containerName: "task",
		command:       []string{"echo", "hi"},
		detach:        false,
		outputText:    "hi",
	}
	result := buildSimulatedContainerRunResult(opts)
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "exited" {
		t.Fatalf("expected status='exited' for detach=false, got %q", output[containerKeyStatus])
	}
}

func TestHandleContainerRunError_NonStrict(t *testing.T) {
	cmdResult := app.CommandResult{ExitCode: 1, Output: "pull failed"}
	result, domErr := handleContainerRunError(
		containerRunContext{
			sessionID: testContainerSessionID1, imageRef: testContainerImageNginxLatest, containerName: "mybox",
			command: []string{testContainerCmdNginx}, envPairs: []string{},
			detach: false, remove: true, strict: false,
		},
		testContainerRuntimeDocker, cmdResult, errors.New("exit 1"),
	)
	if domErr != nil {
		t.Fatalf("expected nil error for non-strict mode, got %v", domErr)
	}
	output := result.Output.(map[string]any)
	if output[containerSourceSimulated] != true {
		t.Fatal("expected simulated=true in non-strict mode")
	}
}

func TestHandleContainerRunError_Strict(t *testing.T) {
	cmdResult := app.CommandResult{ExitCode: 1, Output: "docker error"}
	result, domErr := handleContainerRunError(
		containerRunContext{
			sessionID: testContainerSessionID1, imageRef: testContainerImageNginxLatest, containerName: "mybox",
			command: []string{testContainerCmdNginx}, envPairs: []string{},
			detach: false, remove: true, strict: true,
		},
		testContainerRuntimeDocker, cmdResult, errors.New("exit 1"),
	)
	if domErr == nil {
		t.Fatal("expected domain error for strict mode")
	}
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "failed" {
		t.Fatalf("expected status='failed' for strict mode error, got %q", output[containerKeyStatus])
	}
	if output[containerSourceSimulated] != false {
		t.Fatal("expected simulated=false in strict mode error")
	}
}

