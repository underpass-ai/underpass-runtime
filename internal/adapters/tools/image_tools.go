package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	imageKeyRef               = "image_ref"
	imageKeySummary           = "summary"
	imageBuilderPodman        = "podman"
	imageKeyExitCode          = "exit_code"
	imageBuilderBuildah       = "buildah"
	imageKeyTruncated         = "truncated"
	imageKeyRepository        = "repository"
	imageKeyRegistry          = "registry"
	imageKeyRecommendations   = "recommendations"
	imageKeyOutput            = "output"
	imageKeyIssuesCount       = "issues_count"
	imageKeyIssues            = "issues"
	imageKeyDockerfilePath    = "dockerfile_path"
	imageKeyDigest            = "digest"
	imageKeyContextPath       = "context_path"
	imageKeyCommand           = "command"
	imageKeyPushSkippedReason = "push_skipped_reason"
	imageDefaultDockerfile    = "Dockerfile"
	imageContentTypePlain     = "text/plain"
	imageContentTypeJSON      = "application/json"
	imageFlagNoCache          = "--no-cache"
)

type ImageBuildHandler struct {
	runner app.CommandRunner
}

type ImagePushHandler struct {
	runner app.CommandRunner
}

type ImageInspectHandler struct {
	runner app.CommandRunner
}

func NewImageBuildHandler(runner app.CommandRunner) *ImageBuildHandler {
	return &ImageBuildHandler{runner: runner}
}

func NewImagePushHandler(runner app.CommandRunner) *ImagePushHandler {
	return &ImagePushHandler{runner: runner}
}

func NewImageInspectHandler(runner app.CommandRunner) *ImageInspectHandler {
	return &ImageInspectHandler{runner: runner}
}

func (h *ImageBuildHandler) Name() string {
	return "image.build"
}

func (h *ImagePushHandler) Name() string {
	return "image.push"
}

func (h *ImageBuildHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ContextPath            string `json:"context_path"`
		DockerfilePath         string `json:"dockerfile_path"`
		Tag                    string `json:"tag"`
		Push                   bool   `json:"push"`
		NoCache                bool   `json:"no_cache"`
		MaxIssues              int    `json:"max_issues"`
		IncludeRecommendations bool   `json:"include_recommendations"`
	}{
		ContextPath:            ".",
		DockerfilePath:         imageDefaultDockerfile,
		Push:                   false,
		NoCache:                false,
		MaxIssues:              200,
		IncludeRecommendations: true,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid image.build args",
				Retryable: false,
			}
		}
	}

	contextPath, contextErr := sanitizeRelativePath(request.ContextPath)
	if contextErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   contextErr.Error(),
			Retryable: false,
		}
	}
	if contextPath == "" {
		contextPath = "."
	}

	dockerfilePath, dockerfileErr := sanitizeRelativePath(request.DockerfilePath)
	if dockerfileErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   dockerfileErr.Error(),
			Retryable: false,
		}
	}
	if dockerfilePath == "" {
		dockerfilePath = imageDefaultDockerfile
	}
	effectiveDockerfilePath := resolveImageDockerfilePath(contextPath, dockerfilePath)

	tag := strings.TrimSpace(request.Tag)
	if tag == "" {
		tag = defaultImageBuildTag(session.ID)
	}
	if tagErr := validateImageReference(tag); tagErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   tagErr.Error(),
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	dockerfileResult, dockerfileRunErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "cat",
		Args:     []string{effectiveDockerfilePath},
		MaxBytes: 512 * 1024,
	})

	maxIssues := clampInt(request.MaxIssues, 1, 2000, 200)
	inspectReport := inspectDockerfileContent(
		dockerfileResult.Output,
		contextPath,
		effectiveDockerfilePath,
		maxIssues,
		request.IncludeRecommendations,
	)
	registry, repository, _, _ := parseImageReference(tag)
	digestFromDockerfile := "sha256:" + sha256Hex(dockerfileResult.Output)
	if dockerfileRunErr != nil {
		result := imageBuildResult(
			map[string]any{
				"builder":               "none",
				"simulated":             false,
				imageKeyContextPath:     contextPath,
				imageKeyDockerfilePath:  effectiveDockerfilePath,
				"tag":                   tag,
				imageKeyRef:             tag,
				imageKeyRegistry:        registry,
				imageKeyRepository:      repository,
				imageKeyDigest:          "",
				imageKeyCommand:         []string{"cat", effectiveDockerfilePath},
				"push_command":          []string{},
				"push_requested":        request.Push,
				"pushed":                false,
				imageKeyPushSkippedReason: "",
				imageKeyIssuesCount:     len(inspectReport.Issues),
				imageKeyIssues:          inspectReport.Issues,
				imageKeyRecommendations: inspectReport.Recommendations,
				imageKeyTruncated:       inspectReport.Truncated,
				imageKeyExitCode:        dockerfileResult.ExitCode,
				imageKeySummary:         "image build failed: unable to read Dockerfile",
				imageKeyOutput:          dockerfileResult.Output,
			},
			dockerfileResult.Output,
			dockerfileResult.Output,
		)
		return result, toToolError(dockerfileRunErr, dockerfileResult.Output)
	}

	detectedBuilder := detectImageBuilder(ctx, runner, session)
	issuesCount := len(inspectReport.Issues)
	bs := imageBuildRunWithBuilder(ctx, runner, session, imageBuildRunOptions{
		detectedBuilder:         detectedBuilder,
		contextPath:             contextPath,
		effectiveDockerfilePath: effectiveDockerfilePath,
		tag:                     tag,
		digestFromDockerfile:    digestFromDockerfile,
		noCache:                 request.NoCache,
		push:                    request.Push,
	})

	imageRef := tag
	if bs.imageDigest != "" && !strings.Contains(imageRef, "@") {
		imageRef = imageRef + "@" + bs.imageDigest
	}
	if strings.TrimSpace(bs.buildOutput) == "" {
		bs.buildOutput = bs.summary
	}
	if strings.TrimSpace(bs.logMessage) == "" {
		bs.logMessage = bs.buildOutput
	}

	result := imageBuildResult(
		map[string]any{
			"builder":               bs.builder,
			"simulated":             bs.simulated,
			imageKeyContextPath:     contextPath,
			imageKeyDockerfilePath:  effectiveDockerfilePath,
			"tag":                   tag,
			imageKeyRef:             imageRef,
			imageKeyRegistry:        registry,
			imageKeyRepository:      repository,
			imageKeyDigest:          bs.imageDigest,
			imageKeyCommand:         bs.command,
			"push_command":          bs.pushCommand,
			"push_requested":        request.Push,
			"pushed":                bs.pushed,
			imageKeyPushSkippedReason: bs.pushSkippedReason,
			imageKeyIssuesCount:     issuesCount,
			imageKeyIssues:          inspectReport.Issues,
			imageKeyRecommendations: inspectReport.Recommendations,
			imageKeyTruncated:       inspectReport.Truncated,
			imageKeyExitCode:        bs.exitCode,
			imageKeySummary:         bs.summary,
			imageKeyOutput:          bs.buildOutput,
		},
		bs.buildOutput,
		bs.logMessage,
	)
	if bs.runErr != nil {
		return result, toToolError(bs.runErr, bs.buildOutput)
	}
	return result, nil
}

// imageBuildState holds the mutable state produced by imageBuildRunWithBuilder.
type imageBuildState struct {
	builder           string
	simulated         bool
	command           []string
	pushCommand       []string
	pushed            bool
	pushSkippedReason string
	exitCode          int
	summary           string
	buildOutput       string
	logMessage        string
	imageDigest       string
	runErr            error
}

type imageBuildRunOptions struct {
	detectedBuilder         string
	contextPath             string
	effectiveDockerfilePath string
	tag                     string
	digestFromDockerfile    string
	noCache                 bool
	push                    bool
}

// imageBuildRunWithBuilder executes the image build (and optional push) using
// detectedBuilder, falling back to a synthetic build when the builder is absent
// or the runtime does not support it.
func imageBuildRunWithBuilder(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	opts imageBuildRunOptions,
) imageBuildState {
	detectedBuilder := opts.detectedBuilder
	contextPath := opts.contextPath
	effectiveDockerfilePath := opts.effectiveDockerfilePath
	tag := opts.tag
	digestFromDockerfile := opts.digestFromDockerfile
	noCache := opts.noCache
	push := opts.push
	s := imageBuildState{
		builder:     detectedBuilder,
		summary:     "image build completed",
		imageDigest: digestFromDockerfile,
		command:     []string{},
		pushCommand: []string{},
	}

	if s.builder == "" {
		s.builder = "synthetic"
		s.simulated = true
		s.summary = "image build simulated (no container builder available)"
		s.buildOutput = s.summary
		s.logMessage = s.summary
		if push {
			s.pushSkippedReason = "no_container_builder_available"
		}
		return s
	}

	s.command = buildImageBuildCommand(s.builder, contextPath, effectiveDockerfilePath, tag, noCache)
	buildResult, buildErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  s.command[0],
		Args:     s.command[1:],
		MaxBytes: 2 * 1024 * 1024,
	})
	s.buildOutput = strings.TrimSpace(buildResult.Output)
	s.logMessage = s.buildOutput
	s.exitCode = buildResult.ExitCode
	s.runErr = buildErr
	if digest := extractImageDigest(buildResult.Output); digest != "" {
		s.imageDigest = digest
	}

	if buildErr != nil {
		imageBuildHandleError(&s, buildResult.Output, buildErr, push)
		return s
	}

	if push {
		imageBuildRunPush(ctx, runner, session, &s, tag)
	}
	return s
}

// imageBuildHandleError updates the build state when the build command fails,
// falling back to a synthetic build when the runtime does not support the builder.
func imageBuildHandleError(s *imageBuildState, output string, buildErr error, push bool) {
	if shouldFallbackToSyntheticImageBuild(s.builder, output, buildErr) {
		s.builder = "synthetic"
		s.simulated = true
		s.command = []string{}
		s.pushCommand = []string{}
		s.runErr = nil
		s.exitCode = 0
		s.summary = "image build simulated (builder unavailable in runtime)"
		if push {
			s.pushSkippedReason = "builder_runtime_unavailable"
		}
		return
	}
	s.summary = "image build failed"
	if push {
		s.pushSkippedReason = "build_failed"
	}
}

// imageBuildRunPush executes the push command after a successful build.
func imageBuildRunPush(ctx context.Context, runner app.CommandRunner, session domain.Session, s *imageBuildState, tag string) {
	s.pushCommand = buildImagePushCommand(s.builder, tag)
	pushResult, pushErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  s.pushCommand[0],
		Args:     s.pushCommand[1:],
		MaxBytes: 2 * 1024 * 1024,
	})
	if strings.TrimSpace(pushResult.Output) != "" {
		if strings.TrimSpace(s.buildOutput) != "" {
			s.buildOutput = s.buildOutput + "\n" + pushResult.Output
		} else {
			s.buildOutput = pushResult.Output
		}
	}
	s.logMessage = s.buildOutput
	s.exitCode = pushResult.ExitCode
	if pushErr != nil {
		s.summary = "image push failed"
		s.runErr = pushErr
	} else {
		s.pushed = true
		s.summary = "image build and push completed"
	}
}

func (h *ImagePushHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ImageRef               string `json:"image_ref"`
		MaxRetries             int    `json:"max_retries"`
		Strict                 bool   `json:"strict"`
		MaxIssues              int    `json:"max_issues"`
		IncludeRecommendations bool   `json:"include_recommendations"`
	}{
		MaxRetries:             0,
		Strict:                 false,
		MaxIssues:              200,
		IncludeRecommendations: true,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid image.push args",
				Retryable: false,
			}
		}
	}

	imageRef := strings.TrimSpace(request.ImageRef)
	if imageRef == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "image_ref is required",
			Retryable: false,
		}
	}
	if validateErr := validateImageReference(imageRef); validateErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   validateErr.Error(),
			Retryable: false,
		}
	}

	maxRetries := clampInt(request.MaxRetries, 0, 5, 0)
	maxIssues := clampInt(request.MaxIssues, 1, 2000, 200)
	report := inspectImageReference(imageRef, maxIssues, request.IncludeRecommendations)
	registry, repository, tag, digest := parseImageReference(imageRef)

	runner := ensureRunner(h.runner)
	detectedBuilder := detectImageBuilder(ctx, runner, session)
	ps := imagePushExecute(ctx, runner, session, imagePushExecuteOptions{
		detectedBuilder: detectedBuilder,
		imageRef:        imageRef,
		maxRetries:      maxRetries,
		strict:          request.Strict,
		initialDigest:   digest,
	})

	if ps.strictFailed {
		result := imagePushResult(
			map[string]any{
				"builder":               ps.builder,
				"simulated":             ps.simulated,
				imageKeyRef:             imageRef,
				imageKeyRegistry:        registry,
				imageKeyRepository:      repository,
				"tag":                   tag,
				imageKeyDigest:          ps.digest,
				imageKeyCommand:         ps.command,
				"attempts":              ps.attempts,
				"max_retries":           maxRetries,
				"pushed":                false,
				imageKeyPushSkippedReason: ps.pushSkippedReason,
				imageKeyIssuesCount:     len(report.Issues),
				imageKeyIssues:          report.Issues,
				imageKeyRecommendations: report.Recommendations,
				imageKeyTruncated:       report.Truncated,
				imageKeyExitCode:        ps.exitCode,
				imageKeySummary:         "image push failed: no container builder available in strict mode",
				imageKeyOutput:          ps.outputText,
			},
			ps.outputText,
			ps.logMessage,
		)
		return result, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   "image push failed: no container builder available in strict mode",
			Retryable: false,
		}
	}

	imageRefWithDigest := imageRef
	if ps.digest != "" && !strings.Contains(imageRefWithDigest, "@") {
		imageRefWithDigest = imageRefWithDigest + "@" + ps.digest
	}

	result := imagePushResult(
		map[string]any{
			"builder":               ps.builder,
			"simulated":             ps.simulated,
			imageKeyRef:             imageRefWithDigest,
			imageKeyRegistry:        registry,
			imageKeyRepository:      repository,
			"tag":                   tag,
			imageKeyDigest:          ps.digest,
			imageKeyCommand:         ps.command,
			"attempts":              ps.attempts,
			"max_retries":           maxRetries,
			"pushed":                ps.pushed,
			imageKeyPushSkippedReason: ps.pushSkippedReason,
			imageKeyIssuesCount:     len(report.Issues),
			imageKeyIssues:          report.Issues,
			imageKeyRecommendations: report.Recommendations,
			imageKeyTruncated:       report.Truncated,
			imageKeyExitCode:        ps.exitCode,
			imageKeySummary:         ps.summary,
			imageKeyOutput:          ps.outputText,
		},
		ps.outputText,
		ps.logMessage,
	)
	if ps.runErr != nil {
		return result, toToolError(ps.runErr, ps.outputText)
	}
	return result, nil
}

// imagePushState holds the mutable state produced by imagePushExecute.
type imagePushState struct {
	builder           string
	simulated         bool
	command           []string
	attempts          int
	pushed            bool
	pushSkippedReason string
	exitCode          int
	summary           string
	outputText        string
	logMessage        string
	digest            string
	runErr            error
	strictFailed      bool
}

type imagePushExecuteOptions struct {
	detectedBuilder string
	imageRef        string
	maxRetries      int
	strict          bool
	initialDigest   string
}

// imagePushExecute performs the push (with retries) or marks it as simulated
// when no builder is available. strictFailed is set when strict mode blocks
// a simulated push.
func imagePushExecute(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	opts imagePushExecuteOptions,
) imagePushState {
	detectedBuilder := opts.detectedBuilder
	imageRef := opts.imageRef
	maxRetries := opts.maxRetries
	strict := opts.strict
	initialDigest := opts.initialDigest
	ps := imagePushState{
		builder: detectedBuilder,
		command: []string{},
		digest:  initialDigest,
		summary: "image push completed",
	}

	if ps.builder == "" {
		ps.simulated = true
		ps.builder = "synthetic"
		ps.pushSkippedReason = "no_container_builder_available"
		ps.summary = "image push simulated (no container builder available)"
		ps.outputText = ps.summary
		ps.logMessage = ps.summary
		if strict {
			ps.exitCode = 1
			ps.strictFailed = true
		}
		return ps
	}

	ps.command = buildImagePushCommand(ps.builder, imageRef)
	retryOutputs := make([]string, 0, maxRetries+1)
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		ps.attempts = attempt
		done := imagePushAttempt(ctx, runner, session, &ps, imagePushAttemptConfig{
			imageRef: imageRef, attempt: attempt, totalAttempts: maxRetries + 1, strict: strict,
		}, &retryOutputs)
		if done {
			break
		}
	}
	ps.outputText = strings.TrimSpace(strings.Join(retryOutputs, "\n"))
	ps.logMessage = ps.outputText
	if strings.TrimSpace(ps.outputText) == "" {
		ps.outputText = ps.summary
		ps.logMessage = ps.summary
	}
	return ps
}

// imagePushAttemptConfig groups the per-attempt parameters for a push retry.
type imagePushAttemptConfig struct {
	imageRef      string
	attempt       int
	totalAttempts int
	strict        bool
}

// imagePushAttempt runs a single push attempt, updates ps, appends the
// attempt output to retryOutputs, and returns true when the loop should stop
// (success, graceful fallback, or final failure).
func imagePushAttempt(
	ctx context.Context, runner app.CommandRunner, session domain.Session,
	ps *imagePushState, cfg imagePushAttemptConfig, retryOutputs *[]string,
) bool {
	cmdResult, err := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  ps.command[0],
		Args:     ps.command[1:],
		MaxBytes: 2 * 1024 * 1024,
	})
	ps.exitCode = cmdResult.ExitCode
	if strings.TrimSpace(cmdResult.Output) != "" {
		*retryOutputs = append(*retryOutputs, fmt.Sprintf("[attempt %d/%d]\n%s", cfg.attempt, cfg.totalAttempts, cmdResult.Output))
	}
	if foundDigest := extractImageDigest(cmdResult.Output); foundDigest != "" {
		ps.digest = foundDigest
	}
	if err == nil {
		ps.pushed = true
		ps.summary = "image push completed"
		return true
	}
	if (ps.builder == imageBuilderPodman || ps.builder == imageBuilderBuildah) && isContainerBuilderUserNamespaceUnsupported(cmdResult.Output, err) && !cfg.strict {
		ps.simulated = true
		ps.builder = "synthetic"
		ps.command = []string{}
		ps.pushSkippedReason = "builder_runtime_unavailable"
		ps.summary = "image push simulated (builder unavailable in runtime)"
		ps.exitCode = 0
		return true
	}
	ps.runErr = err
	if cfg.attempt >= cfg.totalAttempts {
		ps.summary = "image push failed"
		return true
	}
	return false
}

func (h *ImageInspectHandler) Name() string {
	return "image.inspect"
}

func (h *ImageInspectHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ContextPath            string `json:"context_path"`
		DockerfilePath         string `json:"dockerfile_path"`
		ImageRef               string `json:"image_ref"`
		IncludeRecommendations bool   `json:"include_recommendations"`
		MaxIssues              int    `json:"max_issues"`
	}{
		ContextPath:            ".",
		DockerfilePath:         imageDefaultDockerfile,
		IncludeRecommendations: true,
		MaxIssues:              200,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid image.inspect args",
				Retryable: false,
			}
		}
	}

	contextPath, contextErr := sanitizeRelativePath(request.ContextPath)
	if contextErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   contextErr.Error(),
			Retryable: false,
		}
	}
	if contextPath == "" {
		contextPath = "."
	}

	dockerfilePath, dockerfileErr := sanitizeRelativePath(request.DockerfilePath)
	if dockerfileErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   dockerfileErr.Error(),
			Retryable: false,
		}
	}
	if dockerfilePath == "" {
		dockerfilePath = imageDefaultDockerfile
	}
	maxIssues := clampInt(request.MaxIssues, 1, 2000, 200)
	imageRef := strings.TrimSpace(request.ImageRef)

	if imageRef != "" {
		report := inspectImageReference(imageRef, maxIssues, request.IncludeRecommendations)
		return imageInspectResult(report, "", nil), nil
	}

	effectiveDockerfilePath := dockerfilePath
	if contextPath != "." {
		effectiveDockerfilePath = strings.TrimPrefix(strings.TrimSpace(contextPath)+"/"+strings.TrimPrefix(strings.TrimSpace(dockerfilePath), "./"), "./")
	}

	runner := ensureRunner(h.runner)
	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "cat",
		Args:     []string{effectiveDockerfilePath},
		MaxBytes: 512 * 1024,
	})

	report := inspectDockerfileContent(commandResult.Output, contextPath, effectiveDockerfilePath, maxIssues, request.IncludeRecommendations)
	result := imageInspectResult(report, commandResult.Output, []string{"cat", effectiveDockerfilePath})
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

type imageInspectReport struct {
	SourceType      string
	ContextPath     string
	DockerfilePath  string
	ImageRef        string
	Registry        string
	Repository      string
	Tag             string
	Digest          string
	BaseImages      []string
	StagesCount     int
	ExposedPorts    []string
	User            string
	Entrypoint      string
	Cmd             string
	Issues          []map[string]any
	Recommendations []string
	Truncated       bool
}

func inspectDockerfileContent(content, contextPath, dockerfilePath string, maxIssues int, includeRecommendations bool) imageInspectReport {
	report := imageInspectReport{
		SourceType:     "dockerfile",
		ContextPath:    contextPath,
		DockerfilePath: dockerfilePath,
		BaseImages:     []string{},
		ExposedPorts:   []string{},
		Issues:         []map[string]any{},
	}

	baseSeen := map[string]struct{}{}
	portSeen := map[string]struct{}{}
	for idx, raw := range strings.Split(content, "\n") {
		inspectDockerfileLine(raw, idx, &report, baseSeen, portSeen)
	}

	if strings.TrimSpace(report.User) == "" {
		report.Issues = appendImageIssue(report.Issues, sweRuleMissingUser, sweSeverityMedium, 0, sweRuleMsgMissingUser, "")
	}
	sortImageIssues(report.Issues)
	if len(report.Issues) > maxIssues {
		report.Issues = report.Issues[:maxIssues]
		report.Truncated = true
	}
	if includeRecommendations {
		report.Recommendations = imageRecommendationsFromIssues(report.Issues)
	}
	return report
}

// inspectDockerfileLine processes a single raw Dockerfile line (at 0-based
// index idx) and updates report, baseSeen, and portSeen in place.
func inspectDockerfileLine(raw string, idx int, report *imageInspectReport, baseSeen map[string]struct{}, portSeen map[string]struct{}) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	upper := strings.ToUpper(line)
	lineNum := idx + 1

	switch {
	case strings.HasPrefix(upper, "FROM "):
		inspectDockerfileFromLine(line, lineNum, report, baseSeen)
	case strings.HasPrefix(upper, "EXPOSE "):
		inspectDockerfileExposeLine(line, report, portSeen)
	case strings.HasPrefix(upper, "USER "):
		report.User = strings.TrimSpace(line[len("USER "):])
	case strings.HasPrefix(upper, "ENTRYPOINT "):
		report.Entrypoint = strings.TrimSpace(line[len("ENTRYPOINT "):])
	case strings.HasPrefix(upper, "CMD "):
		report.Cmd = strings.TrimSpace(line[len("CMD "):])
	case strings.HasPrefix(upper, "ADD "):
		report.Issues = appendImageIssue(report.Issues, sweRuleAddInsteadOfCopy, sweSeverityLow, lineNum, sweRuleMsgAddOverCopy, line)
	case strings.HasPrefix(upper, "RUN "):
		inspectDockerfileRunLine(line, lineNum, report)
	}
}

// inspectDockerfileFromLine processes a FROM instruction, tracking base images
// and recording pinning issues.
func inspectDockerfileFromLine(line string, lineNum int, report *imageInspectReport, baseSeen map[string]struct{}) {
	image := parseFromImage(line)
	if image == "" {
		return
	}
	report.StagesCount++
	if _, exists := baseSeen[image]; !exists {
		baseSeen[image] = struct{}{}
		report.BaseImages = append(report.BaseImages, image)
	}
	if strings.Contains(image, ":latest") {
		report.Issues = appendImageIssue(report.Issues, "dockerfile.unpinned_base_image_latest", sweSeverityMedium, lineNum, "Base image uses mutable latest tag.", line)
	} else if !strings.Contains(image, "@sha256:") && !hasImageTag(image) {
		report.Issues = appendImageIssue(report.Issues, sweRuleUnpinnedBaseImage, sweSeverityMedium, lineNum, sweRuleMsgUnpinnedBase, line)
	}
}

// inspectDockerfileExposeLine processes an EXPOSE instruction, collecting
// unique port tokens.
func inspectDockerfileExposeLine(line string, report *imageInspectReport, portSeen map[string]struct{}) {
	for _, token := range strings.Fields(strings.TrimSpace(line[len("EXPOSE "):])) {
		normalized := strings.TrimSpace(token)
		if normalized == "" {
			continue
		}
		if _, exists := portSeen[normalized]; exists {
			continue
		}
		portSeen[normalized] = struct{}{}
		report.ExposedPorts = append(report.ExposedPorts, normalized)
	}
}

// inspectDockerfileRunLine processes a RUN instruction, recording issues for
// common insecure patterns.
func inspectDockerfileRunLine(line string, lineNum int, report *imageInspectReport) {
	lower := strings.ToLower(line)
	if (strings.Contains(lower, "curl ") || strings.Contains(lower, "wget ")) && strings.Contains(lower, "|") {
		report.Issues = appendImageIssue(report.Issues, sweRulePipeToShell, sweSeverityHigh, lineNum, sweRuleMsgPipeToShell, line)
	}
	if strings.Contains(lower, "chmod 777") {
		report.Issues = appendImageIssue(report.Issues, sweRuleChmod777, sweSeverityMedium, lineNum, sweRuleMsgChmod777, line)
	}
	if strings.Contains(lower, "apt-get install") && !strings.Contains(lower, "--no-install-recommends") {
		report.Issues = appendImageIssue(report.Issues, sweRuleAptRecommends, sweSeverityLow, lineNum, sweRuleMsgAptRecommends, line)
	}
}

func inspectImageReference(imageRef string, maxIssues int, includeRecommendations bool) imageInspectReport {
	registry, repository, tag, digest := parseImageReference(imageRef)
	report := imageInspectReport{
		SourceType:      "image_ref",
		ImageRef:        imageRef,
		Registry:        registry,
		Repository:      repository,
		Tag:             tag,
		Digest:          digest,
		BaseImages:      []string{},
		ExposedPorts:    []string{},
		Issues:          []map[string]any{},
		Recommendations: []string{},
	}

	if tag == "latest" {
		report.Issues = appendImageIssue(report.Issues, "image_ref.latest_tag", sweSeverityMedium, 0, "Image reference uses mutable latest tag.", imageRef)
	}
	if tag == "" && digest == "" {
		report.Issues = appendImageIssue(report.Issues, "image_ref.missing_tag_or_digest", sweSeverityMedium, 0, "Image reference should include a fixed tag or digest.", imageRef)
	}
	if digest == "" {
		report.Issues = appendImageIssue(report.Issues, "image_ref.missing_digest", sweSeverityLow, 0, "Pin image reference with digest for immutable deployments.", imageRef)
	}

	sortImageIssues(report.Issues)
	if len(report.Issues) > maxIssues {
		report.Issues = report.Issues[:maxIssues]
		report.Truncated = true
	}
	if includeRecommendations {
		report.Recommendations = imageRecommendationsFromIssues(report.Issues)
	}
	return report
}

func imageInspectResult(report imageInspectReport, rawOutput string, command []string) app.ToolRunResult {
	issuesCount := len(report.Issues)
	summary := "image inspect completed"
	if report.SourceType == "dockerfile" {
		summary = "dockerfile inspect completed"
	}
	if issuesCount > 0 {
		summary = summary + " with issues"
	}

	output := map[string]any{
		"source_type":     report.SourceType,
		imageKeyContextPath:    report.ContextPath,
		imageKeyDockerfilePath: report.DockerfilePath,
		imageKeyRef:            report.ImageRef,
		imageKeyRegistry:       report.Registry,
		imageKeyRepository:     report.Repository,
		"tag":                  report.Tag,
		imageKeyDigest:         report.Digest,
		imageKeyCommand:        command,
		"base_images":          report.BaseImages,
		"stages_count":         report.StagesCount,
		"exposed_ports":        report.ExposedPorts,
		"user":                 report.User,
		"entrypoint":           report.Entrypoint,
		"cmd":                  report.Cmd,
		imageKeyIssuesCount:    issuesCount,
		imageKeyIssues:         report.Issues,
		imageKeyRecommendations: report.Recommendations,
		imageKeyTruncated:      report.Truncated,
		imageKeyExitCode:       0,
		imageKeySummary:        summary,
		imageKeyOutput:         summary,
	}

	reportBytes, marshalErr := json.MarshalIndent(output, "", "  ")
	artifacts := []app.ArtifactPayload{
		{
			Name:        "image-inspect-report.json",
			ContentType: imageContentTypeJSON,
			Data:        reportBytes,
		},
	}
	if strings.TrimSpace(rawOutput) != "" {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "image-inspect-source.txt",
			ContentType: imageContentTypePlain,
			Data:        []byte(rawOutput),
		})
	}
	if marshalErr != nil {
		artifacts = []app.ArtifactPayload{}
	}

	logMessage := rawOutput
	if strings.TrimSpace(logMessage) == "" {
		logMessage = summary
	}
	return app.ToolRunResult{
		ExitCode:  0,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: logMessage}},
		Output:    output,
		Artifacts: artifacts,
	}
}

func imageBuildResult(output map[string]any, rawOutput, logMessage string) app.ToolRunResult {
	reportBytes, marshalErr := json.MarshalIndent(output, "", "  ")
	artifacts := []app.ArtifactPayload{
		{
			Name:        "image-build-report.json",
			ContentType: imageContentTypeJSON,
			Data:        reportBytes,
		},
	}
	if strings.TrimSpace(rawOutput) != "" {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "image-build-output.txt",
			ContentType: imageContentTypePlain,
			Data:        []byte(rawOutput),
		})
	}
	if marshalErr != nil {
		artifacts = []app.ArtifactPayload{}
	}

	exitCode, _ := output[imageKeyExitCode].(int)
	if strings.TrimSpace(logMessage) == "" {
		logMessage = asString(output[imageKeySummary])
	}
	return app.ToolRunResult{
		ExitCode:  exitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: logMessage}},
		Output:    output,
		Artifacts: artifacts,
	}
}

func imagePushResult(output map[string]any, rawOutput, logMessage string) app.ToolRunResult {
	reportBytes, marshalErr := json.MarshalIndent(output, "", "  ")
	artifacts := []app.ArtifactPayload{
		{
			Name:        "image-push-report.json",
			ContentType: imageContentTypeJSON,
			Data:        reportBytes,
		},
	}
	if strings.TrimSpace(rawOutput) != "" {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        "image-push-output.txt",
			ContentType: imageContentTypePlain,
			Data:        []byte(rawOutput),
		})
	}
	if marshalErr != nil {
		artifacts = []app.ArtifactPayload{}
	}

	exitCode, _ := output[imageKeyExitCode].(int)
	if strings.TrimSpace(logMessage) == "" {
		logMessage = asString(output[imageKeySummary])
	}
	return app.ToolRunResult{
		ExitCode:  exitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: logMessage}},
		Output:    output,
		Artifacts: artifacts,
	}
}

func appendImageIssue(issues []map[string]any, id, severity string, line int, message, snippet string) []map[string]any {
	return append(issues, map[string]any{
		"id":       strings.TrimSpace(id),
		"severity": normalizeFindingSeverity(severity),
		"line":     line,
		"message":  strings.TrimSpace(message),
		"snippet":  truncateString(strings.TrimSpace(snippet), 240),
	})
}

func sortImageIssues(issues []map[string]any) {
	sort.Slice(issues, func(i, j int) bool {
		left := issues[i]
		right := issues[j]
		leftSeverity := normalizeFindingSeverity(asString(left["severity"]))
		rightSeverity := normalizeFindingSeverity(asString(right["severity"]))
		if securitySeverityRank(leftSeverity) != securitySeverityRank(rightSeverity) {
			return securitySeverityRank(leftSeverity) > securitySeverityRank(rightSeverity)
		}
		leftLine, leftLineOK := left["line"].(int)
		rightLine, rightLineOK := right["line"].(int)
		if leftLineOK && rightLineOK && leftLine != rightLine {
			return leftLine < rightLine
		}
		return asString(left["id"]) < asString(right["id"])
	})
}

func imageRecommendationsFromIssues(issues []map[string]any) []string {
	if len(issues) == 0 {
		return []string{"No immediate issues detected for the provided image source."}
	}
	out := make([]string, 0, 6)
	seen := map[string]struct{}{}
	appendUnique := func(value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, issue := range issues {
		switch asString(issue["id"]) {
		case sweRuleUnpinnedBaseImage, "dockerfile.unpinned_base_image_latest", "image_ref.latest_tag", "image_ref.missing_tag_or_digest", "image_ref.missing_digest":
			appendUnique("Pin base images and deployment references with fixed tags and digests.")
		case sweRuleMissingUser:
			appendUnique("Set a non-root USER in Dockerfile runtime stages.")
		case sweRulePipeToShell:
			appendUnique("Avoid piping remote scripts into shell; download, verify, then execute explicitly.")
		case sweRuleChmod777:
			appendUnique("Replace broad permissions like chmod 777 with minimum required permissions.")
		case sweRuleAddInsteadOfCopy:
			appendUnique("Prefer COPY over ADD to keep image layers predictable.")
		case sweRuleAptRecommends:
			appendUnique("Use --no-install-recommends for apt installations.")
		}
	}
	return out
}

func parseFromImage(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return ""
	}
	for _, token := range fields[1:] {
		clean := strings.TrimSpace(token)
		if clean == "" {
			continue
		}
		if strings.HasPrefix(clean, "--") {
			continue
		}
		if strings.EqualFold(clean, "AS") {
			break
		}
		return clean
	}
	return ""
}

func hasImageTag(image string) bool {
	trimmed := strings.TrimSpace(image)
	if trimmed == "" {
		return false
	}
	lastSlash := strings.LastIndex(trimmed, "/")
	lastColon := strings.LastIndex(trimmed, ":")
	return lastColon > lastSlash
}

func parseImageReference(ref string) (string, string, string, string) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", "", "", ""
	}
	namePart := trimmed
	digest := ""
	if index := strings.Index(namePart, "@"); index >= 0 {
		digest = strings.TrimSpace(namePart[index+1:])
		namePart = strings.TrimSpace(namePart[:index])
	}

	tag := ""
	if lastSlash := strings.LastIndex(namePart, "/"); strings.LastIndex(namePart, ":") > lastSlash {
		tagIndex := strings.LastIndex(namePart, ":")
		tag = strings.TrimSpace(namePart[tagIndex+1:])
		namePart = strings.TrimSpace(namePart[:tagIndex])
	}

	registry := "docker.io"
	repository := namePart
	parts := strings.Split(namePart, "/")
	if len(parts) > 1 {
		first := strings.TrimSpace(parts[0])
		if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
			registry = first
			repository = strings.Join(parts[1:], "/")
		}
	}
	return registry, strings.TrimSpace(repository), strings.TrimSpace(tag), strings.TrimSpace(digest)
}

func resolveImageDockerfilePath(contextPath, dockerfilePath string) string {
	effectiveDockerfilePath := dockerfilePath
	if contextPath != "." {
		effectiveDockerfilePath = strings.TrimPrefix(strings.TrimSpace(contextPath)+"/"+strings.TrimPrefix(strings.TrimSpace(dockerfilePath), "./"), "./")
	}
	return effectiveDockerfilePath
}

func defaultImageBuildTag(sessionID string) string {
	candidate := strings.ToLower(strings.TrimSpace(sessionID))
	if candidate == "" {
		return "workspace.local/workspace:latest"
	}
	normalized := make([]rune, 0, len(candidate))
	for _, r := range candidate {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			normalized = append(normalized, r)
		} else {
			normalized = append(normalized, '-')
		}
	}
	value := strings.Trim(strings.Join([]string{"workspace.local/workspace", string(normalized)}, ":"), ":")
	value = strings.ReplaceAll(value, "--", "-")
	if strings.HasSuffix(value, ":") {
		value += "latest"
	}
	if !strings.Contains(value, ":") {
		value += ":latest"
	}
	return value
}

func validateImageReference(ref string) error {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return fmt.Errorf("tag is required")
	}
	if len(trimmed) > 255 {
		return fmt.Errorf("tag exceeds 255 characters")
	}
	for _, r := range trimmed {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			return fmt.Errorf("tag must not contain whitespace")
		}
	}
	if strings.HasPrefix(trimmed, ":") || strings.HasPrefix(trimmed, "@") {
		return fmt.Errorf("tag must include a repository name")
	}
	return nil
}

func detectImageBuilder(ctx context.Context, runner app.CommandRunner, session domain.Session) string {
	candidates := []string{imageBuilderBuildah, imageBuilderPodman, "docker"}
	for _, candidate := range candidates {
		result, err := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  candidate,
			Args:     []string{"version"},
			MaxBytes: 64 * 1024,
		})
		if err == nil && result.ExitCode == 0 {
			return candidate
		}
	}
	return ""
}

func shouldFallbackToSyntheticImageBuild(builder, output string, runErr error) bool {
	normalizedBuilder := strings.TrimSpace(builder)
	return (normalizedBuilder == imageBuilderPodman || normalizedBuilder == imageBuilderBuildah) && isContainerBuilderUserNamespaceUnsupported(output, runErr)
}

func isContainerBuilderUserNamespaceUnsupported(output string, runErr error) bool {
	combined := strings.ToLower(strings.TrimSpace(output))
	if runErr != nil {
		if combined != "" {
			combined += "\n"
		}
		combined += strings.ToLower(strings.TrimSpace(runErr.Error()))
	}
	if combined == "" {
		return false
	}
	return strings.Contains(combined, "unshare(clone_newuser)") ||
		strings.Contains(combined, "cannot clone: operation not permitted") ||
		strings.Contains(combined, "(unable to determine exit status)")
}

func buildImageBuildCommand(builder, contextPath, dockerfilePath, tag string, noCache bool) []string {
	switch builder {
	case imageBuilderBuildah:
		args := []string{imageBuilderBuildah, "bud"}
		if noCache {
			args = append(args, imageFlagNoCache)
		}
		return append(args, []string{"-f", dockerfilePath, "-t", tag, contextPath}...)
	case imageBuilderPodman:
		args := []string{imageBuilderPodman, "build"}
		if noCache {
			args = append(args, imageFlagNoCache)
		}
		return append(args, []string{"-f", dockerfilePath, "-t", tag, contextPath}...)
	default:
		args := []string{"docker", "build"}
		if noCache {
			args = append(args, imageFlagNoCache)
		}
		return append(args, []string{"-f", dockerfilePath, "-t", tag, contextPath}...)
	}
}

func buildImagePushCommand(builder, tag string) []string {
	switch builder {
	case imageBuilderBuildah:
		return []string{imageBuilderBuildah, "push", tag}
	case imageBuilderPodman:
		return []string{imageBuilderPodman, "push", tag}
	default:
		return []string{"docker", "push", tag}
	}
}

var imageDigestPattern = regexp.MustCompile(`sha256:[a-f0-9]{64}`)

func extractImageDigest(output string) string {
	match := imageDigestPattern.FindString(strings.ToLower(output))
	return strings.TrimSpace(match)
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
