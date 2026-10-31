package memory_test

import (
	"context"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/seedgen"
	"testing"
	"time"
)

func TestResultsDoNotMutateStore(t *testing.T) {
	s := seedgen.NewStore(24)
	p, _ := s.GetProduct(context.Background(), 1, false)
	p.Attributes["color"] = "changed"
	p.ImageKeys[0] = "changed"
	again, _ := s.GetProduct(context.Background(), 1, false)
	if again.Attributes["color"] != "black" || again.ImageKeys[0] == "changed" {
		t.Fatal("caller mutated store through returned data")
	}
}
func TestIdempotencyActorAndExpiry(t *testing.T) {
	ctx := context.Background()
	s := seedgen.NewStore(24)
	in := domain.ProductInput{CategoryID: 2, BrandID: 1, Title: "new", Currency: "KZT"}
	k := domain.IdemKey{Actor: "one", Key: "same", Hash: "hash", TTL: 10 * time.Millisecond}
	p, _, err := s.CreateIdempotent(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	k.Actor = "two"
	other, replayed, err := s.CreateIdempotent(ctx, k, in)
	if err != nil || replayed || other.ID == p.ID {
		t.Fatal("actors must have independent keys")
	}
	time.Sleep(20 * time.Millisecond)
	k.Actor = "one"
	again, replayed, err := s.CreateIdempotent(ctx, k, in)
	if err != nil || replayed || again.ID == p.ID {
		t.Fatal("expired key must permit a new operation")
	}
}
