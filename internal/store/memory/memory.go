// Package memory — одно хранилище под mutex, без фоновых задач и внешних служб.
package memory

import (
	"context"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"sort"
	"strings"
	"sync"
	"time"
)

type idemID struct{ actor, key string }
type record struct {
	hash    string
	product domain.Product
	expires time.Time
}
type Store struct {
	mu         sync.RWMutex
	categories []domain.Category
	brands     int
	products   []domain.Product
	keys       map[idemID]record
}

func New(categories []domain.Category, brands int) *Store {
	return &Store{categories: append([]domain.Category(nil), categories...), brands: brands, keys: make(map[idemID]record)}
}
func clone(p domain.Product) domain.Product {
	if p.Attributes != nil {
		attrs := make(map[string]string, len(p.Attributes))
		for k, v := range p.Attributes {
			attrs[k] = v
		}
		p.Attributes = attrs
	}
	p.ImageKeys = append([]string(nil), p.ImageKeys...)
	return p
}
func (s *Store) references(category, brand int64) error {
	valid := false
	for _, c := range s.categories {
		if c.ID == category {
			valid = true
			break
		}
	}
	if !valid || brand < 1 || brand > int64(s.brands) {
		return domain.Errorf(domain.CodeInvalid, "category or brand does not exist")
	}
	return nil
}
func (s *Store) add(in domain.ProductInput) (*domain.Product, error) {
	if err := s.references(in.CategoryID, in.BrandID); err != nil {
		return nil, err
	}
	p := clone(domain.Product{ID: int64(len(s.products) + 1), CategoryID: in.CategoryID, BrandID: in.BrandID, Title: in.Title, Description: in.Description, PriceMinor: in.PriceMinor, Currency: in.Currency, InStock: in.InStock, Attributes: in.Attributes, ImageKeys: in.ImageKeys, Status: domain.StatusActive, Version: 1, UpdatedAt: time.Now().UTC().Truncate(time.Millisecond)})
	s.products = append(s.products, p)
	out := clone(p)
	return &out, nil
}

// Add используется только для начальных демонстрационных данных.
func (s *Store) Add(in domain.ProductInput) (*domain.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.add(in)
}
func (s *Store) GetProduct(ctx context.Context, id int64, hidden bool) (*domain.Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id < 1 || id > int64(len(s.products)) {
		return nil, domain.ErrNotFound
	}
	p := s.products[id-1]
	if p.Status == domain.StatusHidden && !hidden {
		return nil, domain.ErrNotFound
	}
	out := clone(p)
	return &out, nil
}
func (s *Store) ListCategories(ctx context.Context) ([]domain.Category, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]domain.Category(nil), s.categories...), nil
}
func (s *Store) Search(ctx context.Context, q domain.SearchQuery, after *domain.Position) ([]domain.ProductSummary, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	out := make([]domain.ProductSummary, 0)
	for i, p := range s.products {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		if p.Status != domain.StatusActive || q.CategoryID != 0 && q.CategoryID != p.CategoryID || q.BrandID != 0 && q.BrandID != p.BrandID || q.PriceMin != nil && p.PriceMinor < *q.PriceMin || q.PriceMax != nil && p.PriceMinor > *q.PriceMax || !strings.Contains(strings.ToLower(p.Title), strings.ToLower(q.Q)) {
			continue
		}
		if after != nil {
			switch q.Sort {
			case domain.SortPriceAsc:
				if p.PriceMinor < after.Key || p.PriceMinor == after.Key && p.ID <= after.ID {
					continue
				}
			case domain.SortPriceDesc:
				if p.PriceMinor > after.Key || p.PriceMinor == after.Key && p.ID >= after.ID {
					continue
				}
			case domain.SortNewest:
				if p.ID >= after.ID {
					continue
				}
			default:
				if p.ID <= after.ID {
					continue
				}
			}
		}
		item := domain.ProductSummary{ID: p.ID, CategoryID: p.CategoryID, BrandID: p.BrandID, Title: p.Title, PriceMinor: p.PriceMinor, Currency: p.Currency, InStock: p.InStock, Version: p.Version}
		if len(p.ImageKeys) > 0 {
			item.ThumbKey = p.ImageKeys[0]
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch q.Sort {
		case domain.SortPriceAsc:
			if a.PriceMinor != b.PriceMinor {
				return a.PriceMinor < b.PriceMinor
			}
			return a.ID < b.ID
		case domain.SortPriceDesc:
			if a.PriceMinor != b.PriceMinor {
				return a.PriceMinor > b.PriceMinor
			}
			return a.ID > b.ID
		case domain.SortNewest:
			return a.ID > b.ID
		default:
			return a.ID < b.ID
		}
	})
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	return out, more, nil
}
func (s *Store) CreateIdempotent(ctx context.Context, k domain.IdemKey, in domain.ProductInput) (*domain.Product, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := idemID{k.Actor, k.Key}
	now := time.Now()
	// Просроченные ключи больше не нужны: удаляем, чтобы map не росла.
	for kid, r := range s.keys {
		if !now.Before(r.expires) {
			delete(s.keys, kid)
		}
	}
	// Проверка ключа и изменение каталога выполняются в одной критической секции.
	if old, ok := s.keys[id]; ok && now.Before(old.expires) {
		if old.hash != k.Hash {
			return nil, false, domain.ErrKeyReuse
		}
		out := clone(old.product)
		return &out, true, nil
	}
	p, err := s.add(in)
	if err != nil {
		return nil, false, err
	}
	s.keys[id] = record{hash: k.Hash, product: clone(*p), expires: now.Add(k.TTL)}
	return p, false, nil
}
func (s *Store) UpdateProduct(ctx context.Context, id, version int64, p domain.ProductPatch) (*domain.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id < 1 || id > int64(len(s.products)) || s.products[id-1].Status != domain.StatusActive {
		return nil, domain.ErrNotFound
	}
	next := clone(s.products[id-1])
	if next.Version != version {
		return nil, domain.ErrVersionConflict
	}
	if p.CategoryID != nil {
		next.CategoryID = *p.CategoryID
	}
	if p.BrandID != nil {
		next.BrandID = *p.BrandID
	}
	if err := s.references(next.CategoryID, next.BrandID); err != nil {
		return nil, err
	}
	if p.Title != nil {
		next.Title = *p.Title
	}
	if p.Description != nil {
		next.Description = *p.Description
	}
	if p.PriceMinor != nil {
		next.PriceMinor = *p.PriceMinor
	}
	if p.Currency != nil {
		next.Currency = *p.Currency
	}
	if p.InStock != nil {
		next.InStock = *p.InStock
	}
	if p.Attributes != nil {
		next.Attributes = p.Attributes
	}
	next = clone(next)
	next.Version++
	next.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	s.products[id-1] = next
	out := clone(next)
	return &out, nil
}
func (s *Store) HideProduct(ctx context.Context, id, version int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if id < 1 || id > int64(len(s.products)) {
		return 0, domain.ErrNotFound
	}
	p := &s.products[id-1]
	if p.Status == domain.StatusHidden {
		return p.Version, nil
	}
	if p.Version != version {
		return 0, domain.ErrVersionConflict
	}
	p.Status = domain.StatusHidden
	p.Version++
	p.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	return p.Version, nil
}
func (s *Store) Len() int { s.mu.RLock(); defer s.mu.RUnlock(); return len(s.products) }
