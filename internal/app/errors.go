package app

import "net/http"

const (
	ErrorCodeInvalidArgument  = "invalid_argument"
	ErrorCodeNotFound         = "not_found"
	ErrorCodePolicyDenied     = "policy_denied"
	ErrorCodeApprovalRequired = "approval_required"
	ErrorCodeExecutionFailed  = "execution_failed"
	ErrorCodeGitRepoError     = "git_repo_error"
	ErrorCodeGitUsageError    = "git_usage_error"
	ErrorCodeTestsFailed      = "tests_failed"
	ErrorCodeTimeout          = "timeout"
	ErrorCodeInternal         = "internal"
)

type ServiceError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *ServiceError) Error() string {
	return e.Message
}

func notFoundError(message string) *ServiceError {
	return &ServiceError{Code: ErrorCodeNotFound, Message: message, HTTPStatus: http.StatusNotFound}
}

func invalidArgumentError(message string) *ServiceError {
	return &ServiceError{Code: ErrorCodeInvalidArgument, Message: message, HTTPStatus: http.StatusBadRequest}
}

func policyDeniedError(code, message string) *ServiceError {
	status := http.StatusForbidden
	if code == ErrorCodeApprovalRequired {
		status = http.StatusPreconditionRequired
	}
	return &ServiceError{Code: code, Message: message, HTTPStatus: status}
}

func internalError(message string) *ServiceError {
	return &ServiceError{Code: ErrorCodeInternal, Message: message, HTTPStatus: http.StatusInternalServerError}
}
