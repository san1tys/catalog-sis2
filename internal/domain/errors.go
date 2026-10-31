// Package domain содержит типы предметной области и ошибки.
// Он не зависит ни от gRPC, ни от HTTP, ни от хранилищ: оба транспорта и оба
// клиента переводят свои ошибки в эти коды и обратно.
package domain

import (
	"context"
	"errors"
	"fmt"
)

// Code — стабильный код ошибки. Он одинаков в REST (поле error.code), в gRPC
// (trailer x-error-code) и в логах, поэтому клиент может принимать решения
// (повторять или нет) не глядя на протокол.
type Code string

const (
	CodeInvalid         Code = "INVALID_ARGUMENT"      // 400 | INVALID_ARGUMENT
	CodeNotFound        Code = "NOT_FOUND"             // 404 | NOT_FOUND
	CodeKeyReuse        Code = "IDEMPOTENCY_KEY_REUSE" // 409 | ALREADY_EXISTS
	CodeVersionConflict Code = "VERSION_CONFLICT"      // 412 | FAILED_PRECONDITION
	CodeOverloaded      Code = "RESOURCE_EXHAUSTED"    // 429 | RESOURCE_EXHAUSTED
	CodeUnavailable     Code = "UNAVAILABLE"           // 503 | UNAVAILABLE
	CodeDeadline        Code = "DEADLINE_EXCEEDED"     // 504 | DEADLINE_EXCEEDED
	CodeCanceled        Code = "CANCELED"              // 499 | CANCELED
	CodeInternal        Code = "INTERNAL"              // 500 | INTERNAL
)

// Error — ошибка с кодом. RequestID заполняется транспортом.
type Error struct {
	Code      Code
	Message   string
	RequestID string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Is позволяет сравнивать ошибки по коду: errors.Is(err, domain.ErrNotFound).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Errorf создаёт ошибку с форматированным сообщением.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Сентинелы для errors.Is.
var (
	ErrNotFound        = &Error{Code: CodeNotFound, Message: "product not found"}
	ErrKeyReuse        = &Error{Code: CodeKeyReuse, Message: "idempotency key reused with a different request"}
	ErrVersionConflict = &Error{Code: CodeVersionConflict}
	ErrUnavailable     = &Error{Code: CodeUnavailable}
	ErrDeadline        = &Error{Code: CodeDeadline}
)

// CodeOf возвращает код любой ошибки: доменной или контекстной.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var de *Error
	if errors.As(err, &de) {
		return de.Code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return CodeDeadline
	case errors.Is(err, context.Canceled):
		return CodeCanceled
	}
	return CodeInternal
}

// MessageOf возвращает человекочитаемую часть ошибки без кода.
func MessageOf(err error) string {
	var de *Error
	if errors.As(err, &de) {
		return de.Message
	}
	return err.Error()
}

// Retryable: можно ли повторять запрос после такой ошибки.
// Повторяем только то, что не меняет смысл запроса: перегрузка, недоступность,
// истечение deadline попытки. Ошибки клиента (400/404/409/412) не повторяем.
func Retryable(code Code) bool {
	switch code {
	case CodeUnavailable, CodeOverloaded, CodeDeadline:
		return true
	}
	return false
}
