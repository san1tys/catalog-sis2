// Package pbconv переводит доменные типы в protobuf и обратно, а также домен-
// ные ошибки в статусы gRPC и обратно. Им пользуются и gRPC-сервер, и gRPC-клиент.
package pbconv

import (
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/adilbek/catalog-sis2/gen/catalog/v1"
	"github.com/adilbek/catalog-sis2/internal/domain"
)

// Имена метаданных gRPC (аналоги HTTP-заголовков REST).
const (
	MetaIdempotencyKey = "idempotency-key"
	MetaActor          = "x-actor-id"
	MetaRequestID      = "x-request-id"
	TrailerErrorCode   = "x-error-code"
)

// ---- Product ----------------------------------------------------------------

func ProductToPB(p *domain.Product) *pb.Product {
	return &pb.Product{
		Id: p.ID, CategoryId: p.CategoryID, Title: p.Title, BrandId: p.BrandID, Description: p.Description,
		PriceMinor: p.PriceMinor, Currency: p.Currency, InStock: p.InStock, Attributes: p.Attributes,
		Status: string(p.Status), Version: p.Version, UpdatedAtUnixMs: p.UpdatedAt.UnixMilli(), ImageKeys: p.ImageKeys,
	}
}

func ProductFromPB(p *pb.Product) *domain.Product {
	return &domain.Product{
		ID: p.GetId(), CategoryID: p.GetCategoryId(), Title: p.GetTitle(), BrandID: p.GetBrandId(),
		Description: p.GetDescription(), PriceMinor: p.GetPriceMinor(), Currency: p.GetCurrency(),
		InStock: p.GetInStock(), Attributes: p.GetAttributes(), Status: domain.Status(p.GetStatus()),
		Version: p.GetVersion(), UpdatedAt: time.UnixMilli(p.GetUpdatedAtUnixMs()).UTC(), ImageKeys: p.GetImageKeys(),
	}
}

func SummaryToPB(s domain.ProductSummary) *pb.ProductSummary {
	return &pb.ProductSummary{
		Id: s.ID, CategoryId: s.CategoryID, BrandId: s.BrandID, Title: s.Title, PriceMinor: s.PriceMinor,
		Currency: s.Currency, InStock: s.InStock, Version: s.Version, ThumbKey: s.ThumbKey,
	}
}

func SummaryFromPB(s *pb.ProductSummary) domain.ProductSummary {
	return domain.ProductSummary{
		ID: s.GetId(), CategoryID: s.GetCategoryId(), BrandID: s.GetBrandId(), Title: s.GetTitle(),
		PriceMinor: s.GetPriceMinor(), Currency: s.GetCurrency(), InStock: s.GetInStock(),
		Version: s.GetVersion(), ThumbKey: s.GetThumbKey(),
	}
}

// ---- Search -----------------------------------------------------------------

func SearchToPB(q domain.SearchQuery) *pb.SearchRequest {
	return &pb.SearchRequest{
		Q: q.Q, CategoryId: q.CategoryID, BrandId: q.BrandID, PriceMinMinor: q.PriceMin, PriceMaxMinor: q.PriceMax,
		Sort: q.Sort, Limit: int32(q.Limit), Cursor: q.Cursor,
	}
}

func SearchFromPB(r *pb.SearchRequest) domain.SearchQuery {
	return domain.SearchQuery{
		Q: r.GetQ(), CategoryID: r.GetCategoryId(), BrandID: r.GetBrandId(),
		PriceMin: r.PriceMinMinor, PriceMax: r.PriceMaxMinor,
		Sort: r.GetSort(), Limit: int(r.GetLimit()), Cursor: r.GetCursor(),
	}
}

func SearchResultToPB(r *domain.SearchResult) *pb.SearchResponse {
	items := make([]*pb.ProductSummary, len(r.Items))
	for i, it := range r.Items {
		items[i] = SummaryToPB(it)
	}
	return &pb.SearchResponse{Items: items, NextCursor: r.NextCursor}
}

func SearchResultFromPB(r *pb.SearchResponse) *domain.SearchResult {
	items := make([]domain.ProductSummary, len(r.GetItems()))
	for i, it := range r.GetItems() {
		items[i] = SummaryFromPB(it)
	}
	return &domain.SearchResult{Items: items, NextCursor: r.GetNextCursor()}
}

// ---- Categories -------------------------------------------------------------

func CategoriesToPB(l *domain.CategoryList) *pb.ListCategoriesResponse {
	out := make([]*pb.Category, len(l.Categories))
	for i, c := range l.Categories {
		out[i] = &pb.Category{Id: c.ID, ParentId: c.ParentID, Slug: c.Slug, Name: c.Name}
	}
	return &pb.ListCategoriesResponse{Categories: out, Etag: l.ETag}
}

func CategoriesFromPB(r *pb.ListCategoriesResponse) *domain.CategoryList {
	out := make([]domain.Category, len(r.GetCategories()))
	for i, c := range r.GetCategories() {
		out[i] = domain.Category{ID: c.GetId(), ParentID: c.GetParentId(), Slug: c.GetSlug(), Name: c.GetName()}
	}
	return &domain.CategoryList{Categories: out, ETag: r.GetEtag()}
}

// ---- Create / Update --------------------------------------------------------

func InputToPB(in domain.ProductInput) *pb.ProductInput {
	return &pb.ProductInput{
		CategoryId: in.CategoryID, Title: in.Title, BrandId: in.BrandID, Description: in.Description,
		PriceMinor: in.PriceMinor, Currency: in.Currency, InStock: in.InStock, Attributes: in.Attributes, ImageKeys: in.ImageKeys,
	}
}

func InputFromPB(in *pb.ProductInput) domain.ProductInput {
	return domain.ProductInput{
		CategoryID: in.GetCategoryId(), Title: in.GetTitle(), BrandID: in.GetBrandId(), Description: in.GetDescription(),
		PriceMinor: in.GetPriceMinor(), Currency: in.GetCurrency(), InStock: in.GetInStock(),
		Attributes: in.GetAttributes(), ImageKeys: in.GetImageKeys(),
	}
}

func PatchToPB(id, expectedVersion int64, p domain.ProductPatch) *pb.UpdateProductRequest {
	r := &pb.UpdateProductRequest{
		Id: id, ExpectedVersion: expectedVersion,
		CategoryId: p.CategoryID, Title: p.Title, BrandId: p.BrandID, Description: p.Description,
		PriceMinor: p.PriceMinor, Currency: p.Currency, InStock: p.InStock,
	}
	if p.Attributes != nil {
		r.Attributes = &pb.Attributes{Values: p.Attributes}
	}
	return r
}

func PatchFromPB(r *pb.UpdateProductRequest) domain.ProductPatch {
	p := domain.ProductPatch{
		CategoryID: r.CategoryId, Title: r.Title, BrandID: r.BrandId, Description: r.Description,
		PriceMinor: r.PriceMinor, Currency: r.Currency, InStock: r.InStock,
	}
	if r.Attributes != nil {
		p.Attributes = r.Attributes.Values
		if p.Attributes == nil {
			p.Attributes = map[string]string{}
		}
	}
	return p
}

// ---- Ошибки -----------------------------------------------------------------

// GRPCCode — соответствие доменного кода коду gRPC.
func GRPCCode(c domain.Code) codes.Code {
	switch c {
	case domain.CodeInvalid:
		return codes.InvalidArgument
	case domain.CodeNotFound:
		return codes.NotFound
	case domain.CodeKeyReuse:
		return codes.AlreadyExists
	case domain.CodeVersionConflict:
		return codes.FailedPrecondition
	case domain.CodeOverloaded:
		return codes.ResourceExhausted
	case domain.CodeUnavailable:
		return codes.Unavailable
	case domain.CodeDeadline:
		return codes.DeadlineExceeded
	case domain.CodeCanceled:
		return codes.Canceled
	}
	return codes.Internal
}

// DomainCode — обратное соответствие, когда стабильного кода в trailer нет.
func DomainCode(c codes.Code) domain.Code {
	switch c {
	case codes.InvalidArgument:
		return domain.CodeInvalid
	case codes.NotFound:
		return domain.CodeNotFound
	case codes.AlreadyExists:
		return domain.CodeKeyReuse
	case codes.FailedPrecondition:
		return domain.CodeVersionConflict
	case codes.ResourceExhausted:
		return domain.CodeOverloaded
	case codes.Unavailable:
		return domain.CodeUnavailable
	case codes.DeadlineExceeded:
		return domain.CodeDeadline
	case codes.Canceled:
		return domain.CodeCanceled
	}
	return domain.CodeInternal
}

// ToStatusError делает из любой ошибки gRPC-статус. Стабильный код и request id
// сервер кладёт отдельно в trailers (см. grpcserver).
func ToStatusError(err error) error {
	code := domain.CodeOf(err)
	return status.Error(GRPCCode(code), domain.MessageOf(err))
}

// FromStatusError восстанавливает доменную ошибку на стороне клиента.
func FromStatusError(err error, trailer metadata.MD) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return &domain.Error{Code: domain.CodeUnavailable, Message: err.Error()}
	}
	code := DomainCode(st.Code())
	if v := trailer.Get(TrailerErrorCode); len(v) > 0 && v[0] != "" {
		code = domain.Code(v[0])
	}
	de := &domain.Error{Code: code, Message: st.Message()}
	if v := trailer.Get(MetaRequestID); len(v) > 0 {
		de.RequestID = v[0]
	}
	return de
}
