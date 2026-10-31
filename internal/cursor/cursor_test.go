package cursor

import (
	"strings"
	"testing"
	"time"
)

var secret = []byte("test-secret")

func TestRoundtrip(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	p := Payload{Sort: "price_asc", Key: 4200, ID: 77, Filters: "abc", Exp: now.Add(time.Minute).Unix()}
	got, err := Decode(secret, Encode(secret, p), now)
	if err != nil || got != p {
		t.Fatalf("roundtrip: got %+v err=%v", got, err)
	}
}

func TestRejects(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	good := Encode(secret, Payload{Sort: "newest", ID: 5, Exp: now.Add(time.Minute).Unix()})
	cases := []struct {
		name string
		in   string
		at   time.Time
		want error
	}{
		{"expired", good, now.Add(2 * time.Minute), ErrExpired},
		{"empty", "", now, ErrMalformed},
		{"no dot", "abcdef", now, ErrMalformed},
		{"garbage payload", "!!!.!!!", now, ErrMalformed},
		{"tampered payload", forged(good, now), now, ErrSignature},
		{"wrong secret", Encode([]byte("other"), Payload{Exp: now.Add(time.Minute).Unix()}), now, ErrSignature},
	}
	for _, c := range cases {
		if _, err := Decode(secret, c.in, c.at); err != c.want {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// forged берёт подпись настоящего курсора и приклеивает к ней другое содержимое.
func forged(good string, now time.Time) string {
	other := Encode(secret, Payload{Sort: "newest", ID: 999999, Exp: now.Add(time.Minute).Unix()})
	return other[:strings.IndexByte(other, '.')] + good[strings.IndexByte(good, '.'):]
}
