// Package idempotency содержит общие правила ключей идемпотентности.
//
// Хранилище атомарно регистрирует ключ и создаёт товар под одним мьютексом.
// Здесь — то, что
// должно быть одинаковым для всех: хэш запроса, TTL и валидация ключа.
package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/adilbek/catalog-sis2/internal/domain"
)

const (
	// DefaultTTL — срок хранения ключа (контракт SIS #1: 24 часа).
	DefaultTTL = 24 * time.Hour
	MaxKeyLen  = 128
	// AnonymousActor — исполнитель по умолчанию, если транспорт не передал x-actor-id.
	AnonymousActor = "anonymous"
)

// HashInput — sha256 канонического JSON входа. encoding/json сортирует ключи
// map, а Normalize приводит значения к канону, поэтому хэш не зависит от
// протокола, порядка атрибутов и регистра валюты.
func HashInput(in domain.ProductInput) string {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ValidateKey проверяет формат ключа.
func ValidateKey(key string) error {
	if key == "" {
		return domain.Errorf(domain.CodeInvalid, "idempotency key is required")
	}
	if len(key) > MaxKeyLen {
		return domain.Errorf(domain.CodeInvalid, "idempotency key longer than %d bytes", MaxKeyLen)
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return domain.Errorf(domain.CodeInvalid, "idempotency key must be printable ASCII without spaces")
		}
	}
	return nil
}
