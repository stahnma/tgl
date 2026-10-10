package cli

import (
	"errors"
	"fmt"
)

// Exit codes, per the tgl contract:
//
//	0  success
//	1  error
//	4  auth failure
//	8  not found
const (
	CodeOK       = 0
	CodeError    = 1
	CodeAuth     = 4
	CodeNotFound = 8
)

// Error carries an exit code alongside a message.
type Error struct {
	Code int
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil && e.Msg != "" {
		return e.Msg + ": " + e.Err.Error()
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func Wrap(code int, err error, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Err: err}
}

func NotFoundf(format string, args ...any) *Error {
	return Errorf(CodeNotFound, format, args...)
}

func AuthErrorf(format string, args ...any) *Error {
	return Errorf(CodeAuth, format, args...)
}

// CodeOf extracts the exit code an error should produce.
func CodeOf(err error) int {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeError
}
