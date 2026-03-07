package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// ---------------------------------------------------------------------------
// executeGitRemoteCommand
// ---------------------------------------------------------------------------

func TestExecuteGitRemoteCommand_DefaultRemote(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	runner := NewLocalCommandRunner()

	result, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "fetch",
		actionKey: "fetched",
		remote:    "",
		refspec:   "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output[gitKeyRemote] != "origin" {
		t.Fatalf("expected default remote 'origin', got %q", output[gitKeyRemote])
	}
	if output["fetched"] != true {
		t.Fatalf("expected fetched=true, got %#v", output["fetched"])
	}
}

func TestExecuteGitRemoteCommand_RemoteDenied(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{
		WorkspacePath: root,
		AllowedPaths:  []string{"."},
		Metadata: map[string]string{
			"allowed_git_remotes": "origin",
		},
	}
	runner := NewLocalCommandRunner()

	_, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "push",
		actionKey: "pushed",
		remote:    "upstream",
		refspec:   "main",
	})
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy denial for disallowed remote, got %#v", err)
	}
}

func TestExecuteGitRemoteCommand_RefspecDenied(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{
		WorkspacePath: root,
		AllowedPaths:  []string{"."},
		Metadata: map[string]string{
			"allowed_git_remotes":      "origin",
			"allowed_git_ref_prefixes": "refs/heads/release-",
		},
	}
	runner := NewLocalCommandRunner()

	_, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "push",
		actionKey: "pushed",
		remote:    "origin",
		refspec:   "HEAD:refs/heads/feature/nope",
	})
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy denial for disallowed refspec, got %#v", err)
	}
}

func TestExecuteGitRemoteCommand_PushWithFlags(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	runner := NewLocalCommandRunner()

	result, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "push",
		actionKey: "pushed",
		flags:     []string{"-u"},
		remote:    "origin",
		refspec:   "HEAD:refs/heads/main",
	})
	if err != nil {
		t.Fatalf("unexpected push error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["pushed"] != true {
		t.Fatalf("expected pushed=true, got %#v", output["pushed"])
	}
	if output[gitKeyRefspec] != "HEAD:refs/heads/main" {
		t.Fatalf("unexpected refspec in output: %#v", output[gitKeyRefspec])
	}
}

func TestExecuteGitRemoteCommand_PullAfterPush(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	runner := NewLocalCommandRunner()

	// Push first so there's something to pull
	_, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "push",
		actionKey: "pushed",
		remote:    "origin",
		refspec:   "HEAD:refs/heads/main",
	})
	if err != nil {
		t.Fatalf("push setup failed: %#v", err)
	}

	result, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "pull",
		actionKey: "pulled",
		remote:    "origin",
		refspec:   "main",
	})
	if err != nil {
		t.Fatalf("unexpected pull error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["pulled"] != true {
		t.Fatalf("expected pulled=true, got %#v", output["pulled"])
	}
}

func TestExecuteGitRemoteCommand_CommandFails(t *testing.T) {
	root := initGitRepo(t)
	// No remote added — fetch to non-existent remote
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	runner := NewLocalCommandRunner()

	result, err := executeGitRemoteCommand(context.Background(), runner, session, gitRemoteOpts{
		cmdName:   "fetch",
		actionKey: "fetched",
		remote:    "nonexistent",
	})
	if err == nil {
		t.Fatalf("expected error for fetch from non-existent remote, got result: %#v", result.Output)
	}
	// The output should still be present with fetched=false
	output := result.Output.(map[string]any)
	if output["fetched"] != false {
		t.Fatalf("expected fetched=false on failure, got %#v", output["fetched"])
	}
}

// ---------------------------------------------------------------------------
// Push/Fetch/Pull handler integration — validate refactored handlers
// still wire through executeGitRemoteCommand correctly
// ---------------------------------------------------------------------------

func TestGitPushHandler_EmptyRemoteValidation(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{
		WorkspacePath: root,
		AllowedPaths:  []string{"."},
		Metadata: map[string]string{
			"allowed_git_remotes": "origin",
		},
	}

	push := &GitPushHandler{}
	_, err := push.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{}))
	// Empty remote defaults to "origin" which IS in the allowlist, but no remote configured →
	// should fail at git level, not at policy level
	if err != nil && err.Code == app.ErrorCodePolicyDenied {
		t.Fatalf("empty remote should default to origin, not be policy-denied: %#v", err)
	}
}

func TestGitFetchHandler_WithPrune(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	fetch := &GitFetchHandler{}
	result, err := fetch.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{
		"remote": "origin",
		"prune":  true,
		"tags":   true,
	}))
	if err != nil {
		t.Fatalf("unexpected fetch error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["fetched"] != true {
		t.Fatalf("expected fetched=true, got %#v", output["fetched"])
	}
}

func TestGitPullHandler_WithRebase(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)
	// Push initial commit so remote has something
	runGit(t, root, "push", "origin", "HEAD:refs/heads/main")

	// Write a file so there's something on the branch
	filePath := filepath.Join(root, "new.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runGit(t, root, "add", "new.txt")
	runGit(t, root, "commit", "-m", "second commit")

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	pull := &GitPullHandler{}
	result, err := pull.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{
		"remote":  "origin",
		"refspec": "main",
		"rebase":  true,
	}))
	if err != nil {
		t.Fatalf("unexpected pull error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["pulled"] != true {
		t.Fatalf("expected pulled=true, got %#v", output["pulled"])
	}
}

func TestGitPushHandler_ForceWithLease(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	push := &GitPushHandler{}
	result, err := push.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{
		"remote":           "origin",
		"refspec":          "HEAD:refs/heads/main",
		"force_with_lease": true,
		"set_upstream":     true,
	}))
	if err != nil {
		t.Fatalf("unexpected push error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["pushed"] != true {
		t.Fatalf("expected pushed=true, got %#v", output["pushed"])
	}
}

func TestGitPushHandler_InvalidJSON(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	push := &GitPushHandler{}
	_, err := push.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for bad JSON, got %#v", err)
	}
}

func TestGitFetchHandler_InvalidJSON(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	fetch := &GitFetchHandler{}
	_, err := fetch.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for bad JSON, got %#v", err)
	}
}

func TestGitPullHandler_InvalidJSON(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	pull := &GitPullHandler{}
	_, err := pull.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for bad JSON, got %#v", err)
	}
}
