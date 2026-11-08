// Package moniterr is the small set of errors the daemon returns to clients.
package moniterr

import "errors"

// Error is a coded failure safe to show to a local caller.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

// E builds an error.
func E(code, msg string) *Error { return &Error{Code: code, Msg: msg} }

// Code returns the error code, or "internal".
func Code(err error) string {
	var me *Error
	if errors.As(err, &me) && me.Code != "" {
		return me.Code
	}
	return "internal"
}
