package errors

import (
	"errors"
	"strings"
	"testing"
)

func TestCodedError_Error_Plain(t *testing.T) {
	e := New("missing", "no thing", ExitNotFound)
	got := e.Error()
	if !strings.Contains(got, "missing") || !strings.Contains(got, "no thing") {
		t.Errorf("Error() = %q, want code + message", got)
	}
}

func TestCodedError_Error_Wrapped(t *testing.T) {
	inner := errors.New("network down")
	e := Wrap("transport", "request failed", ExitTransport, inner)
	got := e.Error()
	if !strings.Contains(got, "transport") {
		t.Errorf("Error() = %q, missing code", got)
	}
	if !strings.Contains(got, "network down") {
		t.Errorf("Error() = %q, should include wrapped error", got)
	}
}

func TestCodedError_Unwrap(t *testing.T) {
	inner := errors.New("inner")
	e := Wrap("x", "y", 1, inner)
	if got := errors.Unwrap(e); got != inner {
		t.Errorf("Unwrap = %v, want %v", got, inner)
	}
}

func TestCodedError_Unwrap_Nil(t *testing.T) {
	e := New("x", "y", 1)
	if got := errors.Unwrap(e); got != nil {
		t.Errorf("Unwrap with no wrapped = %v, want nil", got)
	}
}

func TestWrap_PreservesFields(t *testing.T) {
	inner := errors.New("inner")
	e := Wrap("code", "msg", ExitConflict, inner)
	if e.Code != "code" || e.Message != "msg" || e.Exit != ExitConflict {
		t.Errorf("Wrap fields = %+v", e)
	}
}
