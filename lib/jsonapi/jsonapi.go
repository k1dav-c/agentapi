// Package jsonapi is the JSON plumbing the launcher's API (lib/host) and the
// hub (lib/hub, lib/dispatch) share: reading a request body, writing an
// answer, and Error, a refusal with its HTTP status and a code clients tell
// apart. Its errors answer ErrorBody, {"code", "message", "error"}: error
// repeats the message, the shape the hub answered before it had codes.
package jsonapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxBody is the most a request body may hold.
const MaxBody = 1 << 20

// Error is a request refused or failed.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Errorf is an Error of status and code.
func Errorf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// IsCode reports whether err is an Error of code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Read reads r's body, at most MaxBody, into v; it answers 400 and reports
// false when it can't.
func Read(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err == nil && len(data) > MaxBody {
		err = errors.New("the body is over 1 MiB")
	}
	if err == nil {
		err = json.Unmarshal(data, v)
	}
	if err != nil {
		WriteError(w, Errorf(http.StatusBadRequest, "bad_request", "read the body: %v", err))
		return false
	}
	return true
}

// Write answers v with status.
func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ErrorBody is an error's answer.
type ErrorBody struct {
	// Code tells the errors apart: one of the API's own, or the HTTP
	// status's text in snake case (StatusCode).
	Code    string `json:"code"`
	Message string `json:"message"`
	// Error is Message again.
	Error string `json:"error"`
}

// StatusCode is the code of an Error that has none of its own: status's
// text in snake case, such as not_found.
func StatusCode(status int) string {
	return strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_"))
}

// WriteError answers err: an Error with its status, anything else 500.
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Status: http.StatusInternalServerError, Code: "internal", Message: err.Error()}
	}
	Write(w, e.Status, ErrorBody{Code: e.Code, Message: e.Message, Error: e.Message})
}

// ReadError reads an error answer's message, from any of its shapes.
func ReadError(data []byte) (code, message string) {
	var body ErrorBody
	if json.Unmarshal(data, &body) != nil {
		return "", ""
	}
	if body.Message == "" {
		body.Message = body.Error
	}
	return body.Code, body.Message
}
