// Package service содержит общую для REST и gRPC логику каталога.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/adilbek/catalog-sis2/internal/cursor"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/idempotency"
)

type Store interface {
	GetProduct(context.Context, int64, bool) (*domain.Product, error)
	ListCategories(context.Context) ([]domain.Category, error)
	Search(context.Context, domain.SearchQuery, *domain.Position) ([]domain.ProductSummary, bool, error)
	CreateIdempotent(context.Context, domain.IdemKey, domain.ProductInput) (*domain.Product, bool, error)
	UpdateProduct(context.Context, int64, int64, domain.ProductPatch) (*domain.Product, error)
	HideProduct(context.Context, int64, int64) (int64, error)
}

type Service struct {
	store  Store
	secret []byte
}

func New(store Store) *Service {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return &Service{store: store, secret: secret}
}

func (s *Service) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	if id <= 0 {
		return nil, domain.Errorf(domain.CodeInvalid, "id must be positive")
	}
	return s.store.GetProduct(ctx, id, false)
}
func (s *Service) GetProductAdmin(ctx context.Context, id int64) (*domain.Product, error) {
	if id <= 0 {
		return nil, domain.Errorf(domain.CodeInvalid, "id must be positive")
	}
	return s.store.GetProduct(ctx, id, true)
}
func (s *Service) ListCategories(ctx context.Context) (*domain.CategoryList, error) {
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(cats)
	h := sha256.Sum256(b)
	return &domain.CategoryList{Categories: cats, ETag: `"` + hex.EncodeToString(h[:8]) + `"`}, nil
}
func (s *Service) Search(ctx context.Context, q domain.SearchQuery) (*domain.SearchResult, error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	normalized := q
	normalized.Cursor = ""
	normalized.Limit = 0
	b, _ := json.Marshal(normalized)
	hash := sha256.Sum256(b)
	filters := hex.EncodeToString(hash[:])
	var after *domain.Position
	if q.Cursor != "" {
		p, err := cursor.Decode(s.secret, q.Cursor, time.Now())
		if err != nil || p.Sort != q.Sort || p.Filters != filters {
			return nil, domain.Errorf(domain.CodeInvalid, "invalid, expired or mismatched cursor")
		}
		after = &domain.Position{Key: p.Key, ID: p.ID}
	}
	items, more, err := s.store.Search(ctx, q, after)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.ProductSummary{}
	}
	result := &domain.SearchResult{Items: items}
	if more && len(items) > 0 {
		last := items[len(items)-1]
		key := last.ID
		if q.Sort == domain.SortPriceAsc || q.Sort == domain.SortPriceDesc {
			key = last.PriceMinor
		}
		result.NextCursor = cursor.Encode(s.secret, cursor.Payload{Sort: q.Sort, Key: key, ID: last.ID, Filters: filters, Exp: time.Now().Add(time.Minute).Unix()})
	}
	return result, nil
}
func (s *Service) CreateProduct(ctx context.Context, actor, key string, in domain.ProductInput) (*domain.Product, bool, error) {
	if err := idempotency.ValidateKey(key); err != nil {
		return nil, false, err
	}
	if actor == "" {
		actor = idempotency.AnonymousActor
	}
	if err := in.Normalize(); err != nil {
		return nil, false, err
	}
	return s.store.CreateIdempotent(ctx, domain.IdemKey{Actor: actor, Key: key, Hash: idempotency.HashInput(in), TTL: idempotency.DefaultTTL}, in)
}
func (s *Service) UpdateProduct(ctx context.Context, id, version int64, patch domain.ProductPatch) (*domain.Product, error) {
	if id <= 0 || version <= 0 {
		return nil, domain.Errorf(domain.CodeInvalid, "id and expected version must be positive")
	}
	if err := patch.Normalize(); err != nil {
		return nil, err
	}
	return s.store.UpdateProduct(ctx, id, version, patch)
}
func (s *Service) HideProduct(ctx context.Context, id, version int64) (int64, error) {
	if id <= 0 || version <= 0 {
		return 0, domain.Errorf(domain.CodeInvalid, "id and expected version must be positive")
	}
	return s.store.HideProduct(ctx, id, version)
}
