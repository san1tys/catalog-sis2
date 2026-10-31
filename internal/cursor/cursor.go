// Package cursor реализует непрозрачный подписанный курсор keyset-пагинации.
//
// Курсор несёт последнюю позицию выдачи (ключ сортировки и id), сортировку,
// хэш фильтров и срок действия. Подпись HMAC-SHA256 не даёт клиенту подделать
// позицию, а привязка к фильтрам и сортировке — «перенести» курсор в другой запрос.
package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Payload struct {
	Sort    string `json:"s"`
	Key     int64  `json:"k"`
	ID      int64  `json:"i"`
	Filters string `json:"f"`
	Exp     int64  `json:"e"` // unix seconds
}

var (
	ErrMalformed = errors.New("cursor: malformed")
	ErrSignature = errors.New("cursor: bad signature")
	ErrExpired   = errors.New("cursor: expired")
)

var enc = base64.RawURLEncoding

func sign(secret, data []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(data)
	return m.Sum(nil)
}

// Encode упаковывает и подписывает курсор.
func Encode(secret []byte, p Payload) string {
	raw, _ := json.Marshal(p)
	return enc.EncodeToString(raw) + "." + enc.EncodeToString(sign(secret, raw))
}

// Decode проверяет подпись и срок действия.
func Decode(secret []byte, s string, now time.Time) (Payload, error) {
	var p Payload
	i := strings.IndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return p, ErrMalformed
	}
	raw, err := enc.DecodeString(s[:i])
	if err != nil {
		return p, ErrMalformed
	}
	sig, err := enc.DecodeString(s[i+1:])
	if err != nil {
		return p, ErrMalformed
	}
	if !hmac.Equal(sig, sign(secret, raw)) {
		return p, ErrSignature
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, ErrMalformed
	}
	if now.Unix() > p.Exp {
		return p, ErrExpired
	}
	return p, nil
}
