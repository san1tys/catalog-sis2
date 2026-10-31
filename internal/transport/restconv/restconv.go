// Package restconv — общие для REST-сервера и REST-клиента константы и
// соответствие доменных кодов HTTP-статусам.
package restconv

import (
	"net/http"

	"github.com/adilbek/catalog-sis2/internal/domain"
)

// Имена HTTP-заголовков (аналоги gRPC metadata, см. pbconv).
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderActor          = "X-Actor-Id"
	HeaderRequestID      = "X-Request-Id"
	HeaderDeadlineMs     = "X-Request-Deadline-Ms" // остаток бюджета времени, мс
	HeaderReplayed       = "Idempotency-Replayed"
)

// ErrorBody — тело ошибки: стабильный code и request_id (контракт SIS #1).
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type Envelope struct {
	Error ErrorBody `json:"error"`
}

// StatusCanceled — нестандартный 499 (клиент закрыл соединение).
const StatusCanceled = 499

// Status — HTTP-статус для доменного кода.
func Status(c domain.Code) int {
	switch c {
	case domain.CodeInvalid:
		return http.StatusBadRequest
	case domain.CodeNotFound:
		return http.StatusNotFound
	case domain.CodeKeyReuse:
		return http.StatusConflict
	case domain.CodeVersionConflict:
		return http.StatusPreconditionFailed
	case domain.CodeOverloaded:
		return http.StatusTooManyRequests
	case domain.CodeUnavailable:
		return http.StatusServiceUnavailable
	case domain.CodeDeadline:
		return http.StatusGatewayTimeout
	case domain.CodeCanceled:
		return StatusCanceled
	}
	return http.StatusInternalServerError
}

// CodeFromStatus — обратное соответствие для ответов без тела ошибки.
func CodeFromStatus(s int) domain.Code {
	switch s {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return domain.CodeInvalid
	case http.StatusNotFound:
		return domain.CodeNotFound
	case http.StatusConflict:
		return domain.CodeKeyReuse
	case http.StatusPreconditionFailed:
		return domain.CodeVersionConflict
	case http.StatusTooManyRequests:
		return domain.CodeOverloaded
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return domain.CodeUnavailable
	case http.StatusGatewayTimeout:
		return domain.CodeDeadline
	case StatusCanceled:
		return domain.CodeCanceled
	}
	return domain.CodeInternal
}
