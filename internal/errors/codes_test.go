package errors

import (
	"errors"
	"testing"
)

func TestExitCodeFor_Nil(t *testing.T) {
	if got := ExitCodeFor(nil); got != 0 {
		t.Errorf("ExitCodeFor(nil) = %d, want 0", got)
	}
}

func TestExitCodeFor_CodedError(t *testing.T) {
	cases := []struct {
		exit int
		want int
	}{
		{ExitNotFound, 3},
		{ExitPermissionDenied, 4},
		{ExitConflict, 5},
		{ExitTransport, 7},
	}
	for _, tc := range cases {
		err := New("x", "y", tc.exit)
		if got := ExitCodeFor(err); got != tc.want {
			t.Errorf("ExitCodeFor exit=%d → %d, want %d", tc.exit, got, tc.want)
		}
	}
}

func TestExitCodeFor_WrappedCodedError(t *testing.T) {
	inner := New("permission", "no", ExitPermissionDenied)
	wrapped := errors.Join(errors.New("ctx"), inner)
	if got := ExitCodeFor(wrapped); got != ExitPermissionDenied {
		t.Errorf("wrapped CodedError → %d, want %d", got, ExitPermissionDenied)
	}
}

func TestExitCodeFor_PlainError(t *testing.T) {
	if got := ExitCodeFor(errors.New("boom")); got != ExitGeneralFailure {
		t.Errorf("plain error → %d, want %d", got, ExitGeneralFailure)
	}
}
