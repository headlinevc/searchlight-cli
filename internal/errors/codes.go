package errors

import (
	"errors"
	"fmt"
)

const (
	ExitSuccess          = 0
	ExitGeneralFailure   = 1
	ExitUsage            = 2
	ExitNotFound         = 3
	ExitPermissionDenied = 4
	ExitConflict         = 5
	ExitTransport        = 7
)

type CodedError struct {
	Code    string
	Message string
	Exit    int
	Tool    string
	Params  map[string]any
	Wrapped error
}

func (e *CodedError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *CodedError) Unwrap() error { return e.Wrapped }

func New(code, msg string, exit int) *CodedError {
	return &CodedError{Code: code, Message: msg, Exit: exit}
}

func Wrap(code, msg string, exit int, err error) *CodedError {
	return &CodedError{Code: code, Message: msg, Exit: exit, Wrapped: err}
}

func ExitCodeFor(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Exit
	}
	return ExitGeneralFailure
}
