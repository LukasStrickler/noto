package notoerr

import (
	"encoding/json"
	"fmt"
)

type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func (e *Error) Error() string {
	return e.Message
}

func New(code, message string, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{Code: code, Message: message, Details: details}
}

func Wrap(code, message string, err error) *Error {
	details := map[string]any{}
	if err != nil {
		details["cause"] = err.Error()
	}
	return New(code, message, details)
}

func WriteJSON(w interface{ Write([]byte) (int, error) }, err error) {
	ne, ok := err.(*Error)
	if !ok {
		ne = New("internal_error", fmt.Sprintf("%v", err), nil)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": ne})
}
