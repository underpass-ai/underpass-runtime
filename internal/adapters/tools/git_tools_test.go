package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestGitHandlers_StatusDiffApplyPatch(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	ctx := context.Background()

	filePath := filepath.Join(root, "main.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2-modified\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	status := &GitStatusHandler{}
	statusResult, statusErr := status.Invoke(ctx, session, json.RawMessage(`{"short":true}`))
	if statusErr != nil {
		t.Fatalf("unexpected git status error: %v", statusErr)
	}
	statusOutput := statusResult.Output.(map[string]any)["status"].(string)
	if !strings.Contains(statusOutput, "main.txt") {
		t.Fatalf("expected modified file in status, got %q", statusOutput)
	}

	diff := &GitDiffHandler{}
	diffResult, diffErr := diff.Invoke(ctx, session, mustJSONGit(t, map[string]any{"paths": []string{"main.txt"}}))
	if diffErr != nil {
		t.Fatalf("unexpected git diff error: %v", diffErr)
	}
	diffOutput := diffResult.Output.(map[string]any)["diff"].(string)
	if !strings.Contains(diffOutput, "diff --git") {
		t.Fatalf("expected unified diff output, got %q", diffOutput)
	}

	patch := "diff --git a/main.txt b/main.txt\nindex c0d0fb4..83db48f 100644\n--- a/main.txt\n+++ b/main.txt\n@@ -1,2 +1,2 @@\n line1\n-line2-modified\n+line-two\n"
	apply := &GitApplyPatchHandler{}
	applyResult, applyErr := apply.Invoke(ctx, session, mustJSONGit(t, map[string]any{"patch": patch, "check": false}))
	if applyErr != nil {
		t.Fatalf("unexpected git apply error: %v", applyErr)
	}
	if applied := applyResult.Output.(map[string]any)["applied"].(bool); !applied {
		t.Fatalf("expected patch to apply: %#v", applyResult.Output)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if !strings.Contains(string(content), "line-two") {
		t.Fatalf("expected patched content, got %q", string(content))
	}
}

func TestGitHandlers_ValidationAndFailures(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	ctx := context.Background()

	status := &GitStatusHandler{}
	_, err := status.Invoke(ctx, session, json.RawMessage(`{"short":"bad"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument error, got %#v", err)
	}

	apply := &GitApplyPatchHandler{}
	_, err = apply.Invoke(ctx, session, json.RawMessage(`{"patch":""}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected missing patch error, got %#v", err)
	}

	_, err = apply.Invoke(ctx, session, mustJSONGit(t, map[string]any{"patch": "bad patch"}))
	if err == nil || (err.Code != app.ErrorCodeInvalidArgument && err.Code != app.ErrorCodeExecutionFailed && err.Code != app.ErrorCodeGitRepoError) {
		t.Fatalf("expected execution/git repo failure, got %#v", err)
	}

	diff := &GitDiffHandler{}
	_, err = diff.Invoke(ctx, session, mustJSONGit(t, map[string]any{"paths": []string{"../outside"}}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy denial for path traversal, got %#v", err)
	}

	commit := &GitCommitHandler{}
	_, err = commit.Invoke(ctx, session, json.RawMessage(`{"message":""}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument on empty commit message, got %#v", err)
	}
}

func TestGitCommitIdentityResolution(t *testing.T) {
	session := domain.Session{
		Principal: domain.Principal{ActorID: "agent-123"},
		Metadata:  map[string]string{},
	}
	if got := resolveGitCommitAuthorName(session); got != "agent-123" {
		t.Fatalf("expected actor-based author name, got %q", got)
	}
	if got := resolveGitCommitAuthorEmail(session); got != "agent-123@workspace.local" {
		t.Fatalf("expected actor-based author email, got %q", got)
	}

	session.Metadata["git_author_name"] = "custom name"
	session.Metadata["git_author_email"] = "custom@example.local"
	if got := resolveGitCommitAuthorName(session); got != "custom name" {
		t.Fatalf("expected metadata author name, got %q", got)
	}
	if got := resolveGitCommitAuthorEmail(session); got != "custom@example.local" {
		t.Fatalf("expected metadata author email, got %q", got)
	}

	if got := sanitizeGitIdentityValue("  with\nnew\rline  "); got != "withnewline" {
		t.Fatalf("unexpected sanitized identity value: %q", got)
	}
}

func TestToToolErrorTimeout(t *testing.T) {
	err := toToolError(context.DeadlineExceeded, "")
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed for plain deadline error, got %s", err.Code)
	}

	err = toToolError(errors.New("command timeout"), "")
	if err.Code != app.ErrorCodeTimeout {
		t.Fatalf("expected timeout code, got %s", err.Code)
	}
}

func TestGitHandlers_LifecycleOperations(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	ctx := context.Background()

	checkout := &GitCheckoutHandler{}
	_, err := checkout.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"ref":    "feature/lifecycle",
		"create": true,
	}))
	if err != nil {
		t.Fatalf("unexpected git checkout error: %#v", err)
	}

	branchList := &GitBranchListHandler{}
	branchesResult, err := branchList.Invoke(ctx, session, mustJSONGit(t, map[string]any{"all": true}))
	if err != nil {
		t.Fatalf("unexpected git branch_list error: %#v", err)
	}
	output := branchesResult.Output.(map[string]any)
	branches, ok := output["branches"].([]map[string]any)
	if !ok {
		rawBranches, okAny := output["branches"].([]any)
		if !okAny {
			t.Fatalf("unexpected branches payload: %#v", output["branches"])
		}
		branches = make([]map[string]any, 0, len(rawBranches))
		for _, item := range rawBranches {
			entry, okEntry := item.(map[string]any)
			if okEntry {
				branches = append(branches, entry)
			}
		}
	}
	foundFeature := false
	for _, branch := range branches {
		if strings.TrimSpace(fmt.Sprintf("%v", branch["name"])) == "feature/lifecycle" {
			foundFeature = true
			break
		}
	}
	if !foundFeature {
		t.Fatalf("expected feature/lifecycle in branch list, got %#v", branches)
	}

	logHandler := &GitLogHandler{}
	logResult, err := logHandler.Invoke(ctx, session, mustJSONGit(t, map[string]any{"ref": "HEAD", "max_count": 5}))
	if err != nil {
		t.Fatalf("unexpected git log error: %#v", err)
	}
	logOutput := logResult.Output.(map[string]any)
	entries, ok := logOutput["entries"].([]map[string]any)
	if !ok {
		rawEntries, okAny := logOutput["entries"].([]any)
		if !okAny {
			t.Fatalf("unexpected log entries payload: %#v", logOutput["entries"])
		}
		entries = make([]map[string]any, 0, len(rawEntries))
		for _, item := range rawEntries {
			entry, okEntry := item.(map[string]any)
			if okEntry {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		t.Fatalf("expected log entries, got %#v", logOutput)
	}

	show := &GitShowHandler{}
	showResult, err := show.Invoke(ctx, session, mustJSONGit(t, map[string]any{"ref": "HEAD"}))
	if err != nil {
		t.Fatalf("unexpected git show error: %#v", err)
	}
	showOutput := showResult.Output.(map[string]any)["show"].(string)
	if !strings.Contains(showOutput, "initial") {
		t.Fatalf("expected commit summary in git show output, got %q", showOutput)
	}

	filePath := filepath.Join(root, "main.txt")
	if err := os.WriteFile(filePath, []byte("line1\\nline2-lifecycle\\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	commit := &GitCommitHandler{}
	commitResult, err := commit.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"message": "feat: lifecycle commit",
		"all":     true,
	}))
	if err != nil {
		t.Fatalf("unexpected git commit error: %#v", err)
	}
	commitOutput := commitResult.Output.(map[string]any)
	if committed, ok := commitOutput["committed"].(bool); !ok || !committed {
		t.Fatalf("expected committed=true, got %#v", commitOutput)
	}
	if strings.TrimSpace(fmt.Sprintf("%v", commitOutput["commit"])) == "" {
		t.Fatalf("expected non-empty commit hash, got %#v", commitOutput)
	}

	fetch := &GitFetchHandler{}
	_, err = fetch.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"remote": "origin",
		"prune":  true,
	}))
	if err != nil {
		t.Fatalf("unexpected git fetch error: %#v", err)
	}

	push := &GitPushHandler{}
	_, err = push.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"remote":       "origin",
		"refspec":      "HEAD:refs/heads/feature/lifecycle",
		"set_upstream": true,
	}))
	if err != nil {
		t.Fatalf("unexpected git push error: %#v", err)
	}

	pull := &GitPullHandler{}
	_, err = pull.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"remote":  "origin",
		"refspec": "feature/lifecycle",
	}))
	if err != nil {
		t.Fatalf("unexpected git pull error: %#v", err)
	}
}

func TestGitHandlers_AllowlistPolicies(t *testing.T) {
	root := initGitRepo(t)
	remotePath := initBareGitRepo(t)
	runGit(t, root, "remote", "add", "origin", remotePath)

	session := domain.Session{
		WorkspacePath: root,
		AllowedPaths:  []string{"."},
		Metadata: map[string]string{
			"allowed_git_remotes":      "origin",
			"allowed_git_ref_prefixes": "refs/heads/release-,refs/tags/release-",
		},
	}
	ctx := context.Background()

	checkout := &GitCheckoutHandler{}
	_, err := checkout.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"ref": "feature/nope",
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected ref allowlist denial on checkout, got %#v", err)
	}

	push := &GitPushHandler{}
	_, err = push.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"remote":  "upstream",
		"refspec": "HEAD:refs/heads/release-2026",
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected remote allowlist denial on push, got %#v", err)
	}

	_, err = push.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"remote":  "origin",
		"refspec": "HEAD:refs/heads/feature/nope",
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected ref allowlist denial on push, got %#v", err)
	}

	fetch := &GitFetchHandler{}
	_, err = fetch.Invoke(ctx, session, mustJSONGit(t, map[string]any{
		"remote":  "origin",
		"refspec": "refs/heads/release-2026",
	}))
	if err != nil && err.Code == app.ErrorCodePolicyDenied {
		t.Fatalf("did not expect policy denial for allowed fetch refspec, got %#v", err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "tester@example.com")
	runGit(t, root, "config", "user.name", "Tester")

	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write seed file failed: %v", err)
	}
	runGit(t, root, "add", "main.txt")
	runGit(t, root, "commit", "-m", "initial")

	return root
}

func initBareGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	runGit(t, root, "init", "--bare", remotePath)
	return remotePath
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(output))
	}
}

func mustJSONGit(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return data
}
