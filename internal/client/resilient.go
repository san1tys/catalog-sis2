package client

import (
	"context"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"math/rand"
	"time"
)

// Resilient задаёт одинаковые таймауты и повторы для обоих протоколов.
// Автоматически повторяются только Create с тем же ключом и Hide.
type Resilient struct {
	Next                                         Catalog
	ReadTimeout, WriteAttemptTimeout, WriteTotal time.Duration
	MaxAttempts                                  int
	BaseDelay, MaxDelay                          time.Duration
}

func NewResilient(next Catalog) *Resilient {
	return &Resilient{Next: next, ReadTimeout: 80 * time.Millisecond, WriteAttemptTimeout: time.Second, WriteTotal: 5 * time.Second, MaxAttempts: 3, BaseDelay: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond}
}
func read[T any](ctx context.Context, timeout time.Duration, f func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return f(ctx)
}
func write[T any](ctx context.Context, r *Resilient, retry bool, f func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, r.WriteTotal)
	defer cancel()
	var zero T
	attempts := r.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	if !retry {
		attempts = 1
	}
	for n := 0; n < attempts; n++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		actx, stop := context.WithTimeout(ctx, r.WriteAttemptTimeout)
		value, err := f(actx)
		stop()
		if err == nil {
			return value, nil
		}
		if !domain.Retryable(domain.CodeOf(err)) || ctx.Err() != nil || n == attempts-1 {
			return zero, err
		}
		delay := r.BaseDelay
		for i := 0; i < n && delay < r.MaxDelay; i++ {
			if delay > r.MaxDelay/2 {
				delay = r.MaxDelay
			} else {
				delay *= 2
			}
		}
		if delay > r.MaxDelay {
			delay = r.MaxDelay
		}
		if delay > 0 {
			delay = time.Duration(rand.Int63n(int64(delay) + 1))
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		}
	}
	return zero, domain.ErrUnavailable
}
func (r *Resilient) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	return read(ctx, r.ReadTimeout, func(c context.Context) (*domain.Product, error) { return r.Next.GetProduct(c, id) })
}
func (r *Resilient) GetProductAdmin(ctx context.Context, id int64) (*domain.Product, error) {
	return read(ctx, r.ReadTimeout, func(c context.Context) (*domain.Product, error) { return r.Next.GetProductAdmin(c, id) })
}
func (r *Resilient) ListCategories(ctx context.Context) (*domain.CategoryList, error) {
	return read(ctx, r.ReadTimeout, r.Next.ListCategories)
}
func (r *Resilient) Search(ctx context.Context, q domain.SearchQuery) (*domain.SearchResult, error) {
	return read(ctx, r.ReadTimeout, func(c context.Context) (*domain.SearchResult, error) { return r.Next.Search(c, q) })
}

type created struct {
	product  *domain.Product
	replayed bool
}

func (r *Resilient) CreateProduct(ctx context.Context, key string, in domain.ProductInput) (*domain.Product, bool, error) {
	result, err := write(ctx, r, true, func(c context.Context) (created, error) {
		p, replayed, err := r.Next.CreateProduct(c, key, in)
		return created{p, replayed}, err
	})
	return result.product, result.replayed, err
}
func (r *Resilient) UpdateProduct(ctx context.Context, id, version int64, p domain.ProductPatch) (*domain.Product, error) {
	return write(ctx, r, false, func(c context.Context) (*domain.Product, error) { return r.Next.UpdateProduct(c, id, version, p) })
}
func (r *Resilient) HideProduct(ctx context.Context, id, version int64) (int64, error) {
	return write(ctx, r, true, func(c context.Context) (int64, error) { return r.Next.HideProduct(c, id, version) })
}
func (r *Resilient) Close() error { return r.Next.Close() }
