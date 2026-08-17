// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import "fmt"

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrNoSessionBus     Error = "dbus: DBUS_SESSION_BUS_ADDRESS is not set"
	ErrAuthFailed       Error = "dbus: EXTERNAL authentication rejected"
	ErrClosed           Error = "dbus: connection closed"
	ErrCallTimeout      Error = "dbus: call timed out"
	ErrUnsupportedType  Error = "dbus: unsupported type"
	ErrSignatureMismatch Error = "dbus: signature mismatch"
)

type CallError struct {
	Name string // e.g. "org.freedesktop.DBus.Error.ServiceUnknown"
	Msg  string
}

func (e *CallError) Error() string { return e.Name + ": " + e.Msg }

type ErrorResponse struct {
	Name string
	Body []any
}

func (e *ErrorResponse) Error() string {
	if len(e.Body) >= 1 {
		if s, ok := e.Body[0].(string); ok {
			return s
		}
	}
	return e.Name
}

type InvalidTypeError struct {
	Type fmt.Stringer
}

func (e InvalidTypeError) Error() string {
	return "dbus: unsupported type " + e.Type.String()
}
