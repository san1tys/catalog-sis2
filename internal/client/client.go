// Package client описывает клиентский интерфейс каталога. Его реализуют gRPC-
// и REST-клиенты; Resilient добавляет к любой реализации таймауты и retry.
// Один интерфейс позволяет прогнать один и тот же набор тестов и один и тот же
// бенчмарк по всем протоколам.
package client

import (
	"context"

	"github.com/adilbek/catalog-sis2/internal/domain"
)

type Catalog interface {
	GetProduct(ctx context.Context, id int64) (*domain.Product, error)
	GetProductAdmin(ctx context.Context, id int64) (*domain.Product, error)
	ListCategories(ctx context.Context) (*domain.CategoryList, error)
	Search(ctx context.Context, q domain.SearchQuery) (*domain.SearchResult, error)
	// CreateProduct идемпотентен по key; replayed=true — вернулся сохранённый результат.
	CreateProduct(ctx context.Context, key string, in domain.ProductInput) (p *domain.Product, replayed bool, err error)
	UpdateProduct(ctx context.Context, id, expectedVersion int64, patch domain.ProductPatch) (*domain.Product, error)
	HideProduct(ctx context.Context, id, expectedVersion int64) (int64, error)
	Close() error
}
