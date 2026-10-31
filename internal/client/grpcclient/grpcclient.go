// Package grpcclient — клиент Catalog API поверх gRPC.
package grpcclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "github.com/adilbek/catalog-sis2/gen/catalog/v1"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/idempotency"
	"github.com/adilbek/catalog-sis2/internal/transport/pbconv"
)

type Options struct {
	Addr  string
	Actor string
}
type Client struct {
	conn  *grpc.ClientConn
	api   pb.CatalogServiceClient
	actor string
}

func New(ctx context.Context, o Options) (*Client, error) {
	if o.Actor == "" {
		o.Actor = idempotency.AnonymousActor
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, o.Addr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, &domain.Error{Code: domain.CodeUnavailable, Message: err.Error()}
	}
	return &Client{conn: conn, api: pb.NewCatalogServiceClient(conn), actor: o.Actor}, nil
}
func (c *Client) Close() error { return c.conn.Close() }

// invoke вызывает rpc и превращает ошибку статуса в доменную (с кодом из trailers).
func invoke[T any](f func(trailer *metadata.MD) (T, error)) (T, error) {
	var tr metadata.MD
	v, err := f(&tr)
	if err != nil {
		var zero T
		return zero, pbconv.FromStatusError(err, tr)
	}
	return v, nil
}

func (c *Client) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	return invoke(func(tr *metadata.MD) (*domain.Product, error) {
		p, err := c.api.GetProduct(ctx, &pb.GetProductRequest{Id: id}, grpc.Trailer(tr))
		if err != nil {
			return nil, err
		}
		return pbconv.ProductFromPB(p), nil
	})
}

func (c *Client) GetProductAdmin(ctx context.Context, id int64) (*domain.Product, error) {
	return invoke(func(tr *metadata.MD) (*domain.Product, error) {
		p, err := c.api.GetProductAdmin(ctx, &pb.GetProductRequest{Id: id}, grpc.Trailer(tr))
		if err != nil {
			return nil, err
		}
		return pbconv.ProductFromPB(p), nil
	})
}

func (c *Client) ListCategories(ctx context.Context) (*domain.CategoryList, error) {
	return invoke(func(tr *metadata.MD) (*domain.CategoryList, error) {
		r, err := c.api.ListCategories(ctx, &pb.ListCategoriesRequest{}, grpc.Trailer(tr))
		if err != nil {
			return nil, err
		}
		return pbconv.CategoriesFromPB(r), nil
	})
}

func (c *Client) Search(ctx context.Context, q domain.SearchQuery) (*domain.SearchResult, error) {
	return invoke(func(tr *metadata.MD) (*domain.SearchResult, error) {
		r, err := c.api.Search(ctx, pbconv.SearchToPB(q), grpc.Trailer(tr))
		if err != nil {
			return nil, err
		}
		return pbconv.SearchResultFromPB(r), nil
	})
}

type created struct {
	p        *domain.Product
	replayed bool
}

func (c *Client) CreateProduct(ctx context.Context, key string, in domain.ProductInput) (*domain.Product, bool, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, pbconv.MetaIdempotencyKey, key, pbconv.MetaActor, c.actor)
	res, err := invoke(func(tr *metadata.MD) (created, error) {
		r, err := c.api.CreateProduct(ctx, &pb.CreateProductRequest{Input: pbconv.InputToPB(in)}, grpc.Trailer(tr))
		if err != nil {
			return created{}, err
		}
		return created{pbconv.ProductFromPB(r.GetProduct()), r.GetReplayed()}, nil
	})
	return res.p, res.replayed, err
}

func (c *Client) UpdateProduct(ctx context.Context, id, expectedVersion int64, patch domain.ProductPatch) (*domain.Product, error) {
	return invoke(func(tr *metadata.MD) (*domain.Product, error) {
		p, err := c.api.UpdateProduct(ctx, pbconv.PatchToPB(id, expectedVersion, patch), grpc.Trailer(tr))
		if err != nil {
			return nil, err
		}
		return pbconv.ProductFromPB(p), nil
	})
}

func (c *Client) HideProduct(ctx context.Context, id, expectedVersion int64) (int64, error) {
	return invoke(func(tr *metadata.MD) (int64, error) {
		r, err := c.api.HideProduct(ctx, &pb.HideProductRequest{Id: id, ExpectedVersion: expectedVersion}, grpc.Trailer(tr))
		if err != nil {
			return 0, err
		}
		return r.GetVersion(), nil
	})
}
