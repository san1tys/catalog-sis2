// Package grpcserver реализует gRPC/protobuf поверх HTTP/2.
package grpcserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	pb "github.com/adilbek/catalog-sis2/gen/catalog/v1"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/service"
	"github.com/adilbek/catalog-sis2/internal/transport/pbconv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"strings"
	"time"
)

type Server struct {
	pb.UnimplementedCatalogServiceServer
	svc *service.Service
}

func New(svc *service.Service, timeout time.Duration) *grpc.Server {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	s := &Server{svc: svc}
	gs := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		if !strings.HasPrefix(info.FullMethod, "/catalog.v1.CatalogService/") {
			return h(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var id [8]byte
		_, _ = rand.Read(id[:])
		rid := hex.EncodeToString(id[:])
		resp, err := h(ctx, req)
		if err != nil {
			_ = grpc.SetTrailer(ctx, metadata.Pairs(pbconv.TrailerErrorCode, string(domain.CodeOf(err)), pbconv.MetaRequestID, rid))
			return nil, pbconv.ToStatusError(err)
		}
		return resp, nil
	}))
	pb.RegisterCatalogServiceServer(gs, s)
	reflection.Register(gs)
	return gs
}
func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}
func (s *Server) GetProduct(ctx context.Context, r *pb.GetProductRequest) (*pb.Product, error) {
	p, err := s.svc.GetProduct(ctx, r.GetId())
	if err != nil {
		return nil, err
	}
	return pbconv.ProductToPB(p), nil
}

func (s *Server) GetProductAdmin(ctx context.Context, r *pb.GetProductRequest) (*pb.Product, error) {
	p, err := s.svc.GetProductAdmin(ctx, r.GetId())
	if err != nil {
		return nil, err
	}
	return pbconv.ProductToPB(p), nil
}

func (s *Server) ListCategories(ctx context.Context, _ *pb.ListCategoriesRequest) (*pb.ListCategoriesResponse, error) {
	l, err := s.svc.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	return pbconv.CategoriesToPB(l), nil
}

func (s *Server) Search(ctx context.Context, r *pb.SearchRequest) (*pb.SearchResponse, error) {
	res, err := s.svc.Search(ctx, pbconv.SearchFromPB(r))
	if err != nil {
		return nil, err
	}
	return pbconv.SearchResultToPB(res), nil
}

func (s *Server) CreateProduct(ctx context.Context, r *pb.CreateProductRequest) (*pb.CreateProductResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if r.GetInput() == nil {
		return nil, domain.Errorf(domain.CodeInvalid, "input is required")
	}
	p, replayed, err := s.svc.CreateProduct(ctx, first(md, pbconv.MetaActor), first(md, pbconv.MetaIdempotencyKey), pbconv.InputFromPB(r.Input))
	if err != nil {
		return nil, err
	}
	return &pb.CreateProductResponse{Product: pbconv.ProductToPB(p), Replayed: replayed}, nil
}

func (s *Server) UpdateProduct(ctx context.Context, r *pb.UpdateProductRequest) (*pb.Product, error) {
	p, err := s.svc.UpdateProduct(ctx, r.GetId(), r.GetExpectedVersion(), pbconv.PatchFromPB(r))
	if err != nil {
		return nil, err
	}
	return pbconv.ProductToPB(p), nil
}

func (s *Server) HideProduct(ctx context.Context, r *pb.HideProductRequest) (*pb.HideProductResponse, error) {
	v, err := s.svc.HideProduct(ctx, r.GetId(), r.GetExpectedVersion())
	if err != nil {
		return nil, err
	}
	return &pb.HideProductResponse{Version: v}, nil
}
