package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

type Status string

const (
	StatusActive Status = "active"
	StatusHidden Status = "hidden"
)

// Product — полная карточка. JSON-теги задают REST-представление.
type Product struct {
	ID          int64             `json:"id"`
	CategoryID  int64             `json:"category_id"`
	Title       string            `json:"title"`
	BrandID     int64             `json:"brand_id"`
	Description string            `json:"description"`
	PriceMinor  int64             `json:"price_minor"`
	Currency    string            `json:"currency"`
	InStock     bool              `json:"in_stock"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Status      Status            `json:"status"`
	Version     int64             `json:"version"`
	UpdatedAt   time.Time         `json:"updated_at"`
	ImageKeys   []string          `json:"image_keys,omitempty"`
}

// ProductSummary — компактная проекция для выдачи поиска.
type ProductSummary struct {
	ID         int64  `json:"id"`
	CategoryID int64  `json:"category_id"`
	BrandID    int64  `json:"brand_id"`
	Title      string `json:"title"`
	PriceMinor int64  `json:"price_minor"`
	Currency   string `json:"currency"`
	InStock    bool   `json:"in_stock"`
	Version    int64  `json:"version"`
	ThumbKey   string `json:"thumb_key,omitempty"`
}

type Category struct {
	ID       int64  `json:"id"`
	ParentID int64  `json:"parent_id"` // 0 = корень
	Slug     string `json:"slug"`
	Name     string `json:"name"`
}

// CategoryList — ответ ListCategories: список и ETag для условных запросов.
type CategoryList struct {
	Categories []Category `json:"categories"`
	ETag       string     `json:"-"`
}

// ProductInput — данные для создания товара.
type ProductInput struct {
	CategoryID  int64             `json:"category_id"`
	Title       string            `json:"title"`
	BrandID     int64             `json:"brand_id"`
	Description string            `json:"description"`
	PriceMinor  int64             `json:"price_minor"`
	Currency    string            `json:"currency"`
	InStock     bool              `json:"in_stock"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	ImageKeys   []string          `json:"image_keys,omitempty"`
}

// ProductPatch — частичное обновление. nil = поле не меняется.
// Attributes != nil полностью заменяет атрибуты.
type ProductPatch struct {
	CategoryID  *int64            `json:"category_id,omitempty"`
	Title       *string           `json:"title,omitempty"`
	BrandID     *int64            `json:"brand_id,omitempty"`
	Description *string           `json:"description,omitempty"`
	PriceMinor  *int64            `json:"price_minor,omitempty"`
	Currency    *string           `json:"currency,omitempty"`
	InStock     *bool             `json:"in_stock,omitempty"`
	Attributes  map[string]string `json:"attributes"`
}

// IsEmpty: в патче нет ни одного поля.
func (p ProductPatch) IsEmpty() bool {
	return p.CategoryID == nil && p.Title == nil && p.BrandID == nil &&
		p.Description == nil && p.PriceMinor == nil && p.Currency == nil &&
		p.InStock == nil && p.Attributes == nil
}

// Сортировки поиска.
const (
	SortRelevance = "relevance"
	SortPriceAsc  = "price_asc"
	SortPriceDesc = "price_desc"
	SortNewest    = "newest"
)

const (
	DefaultLimit = 24
	MaxLimit     = 48
	MaxQueryLen  = 128
)

// SearchQuery — параметры поиска. PriceMin/PriceMax == nil означает «не задано».
type SearchQuery struct {
	Q          string
	CategoryID int64
	BrandID    int64
	PriceMin   *int64
	PriceMax   *int64
	Sort       string
	Limit      int
	Cursor     string
}

type SearchResult struct {
	Items      []ProductSummary `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// Position — позиция keyset-пагинации: ключ сортировки и id как tie-breaker.
type Position struct {
	Key int64
	ID  int64
}

// IdemKey — ключ идемпотентности, привязанный к исполнителю.
type IdemKey struct {
	Actor string
	Key   string
	Hash  string // sha256 канонического тела запроса
	TTL   time.Duration
}

const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 10000
	MaxAttributes     = 30
	MaxImageKeys      = 20
	MaxPriceMinor     = int64(1) << 50
)

// Normalize приводит вход к каноническому виду и проверяет его.
// Каноническая форма нужна, чтобы request_hash у gRPC и REST совпадал.
func (in *ProductInput) Normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "KZT"
	}
	if len(in.Attributes) == 0 {
		in.Attributes = nil
	}
	if len(in.ImageKeys) == 0 {
		in.ImageKeys = nil
	}
	switch {
	case in.Title == "":
		return Errorf(CodeInvalid, "title is required")
	case utf8.RuneCountInString(in.Title) > MaxTitleLen:
		return Errorf(CodeInvalid, "title longer than %d characters", MaxTitleLen)
	case utf8.RuneCountInString(in.Description) > MaxDescriptionLen:
		return Errorf(CodeInvalid, "description longer than %d characters", MaxDescriptionLen)
	case in.PriceMinor < 0 || in.PriceMinor > MaxPriceMinor:
		return Errorf(CodeInvalid, "price_minor out of range")
	case in.CategoryID <= 0:
		return Errorf(CodeInvalid, "category_id is required")
	case in.BrandID <= 0:
		return Errorf(CodeInvalid, "brand_id is required")
	}
	if err := validateCurrency(in.Currency); err != nil {
		return err
	}
	if err := validateAttrs(in.Attributes); err != nil {
		return err
	}
	return validateImages(in.ImageKeys)
}

// Normalize для патча: те же правила, но только для заданных полей.
func (p *ProductPatch) Normalize() error {
	if p.IsEmpty() {
		return Errorf(CodeInvalid, "patch has no fields")
	}
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		p.Title = &t
		if t == "" || utf8.RuneCountInString(t) > MaxTitleLen {
			return Errorf(CodeInvalid, "title must be 1..%d characters", MaxTitleLen)
		}
	}
	if p.Description != nil && utf8.RuneCountInString(*p.Description) > MaxDescriptionLen {
		return Errorf(CodeInvalid, "description longer than %d characters", MaxDescriptionLen)
	}
	if p.PriceMinor != nil && (*p.PriceMinor < 0 || *p.PriceMinor > MaxPriceMinor) {
		return Errorf(CodeInvalid, "price_minor out of range")
	}
	if p.CategoryID != nil && *p.CategoryID <= 0 {
		return Errorf(CodeInvalid, "category_id must be positive")
	}
	if p.BrandID != nil && *p.BrandID <= 0 {
		return Errorf(CodeInvalid, "brand_id must be positive")
	}
	if p.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*p.Currency))
		p.Currency = &c
		if err := validateCurrency(c); err != nil {
			return err
		}
	}
	return validateAttrs(p.Attributes)
}

func validateCurrency(c string) error {
	if c != "KZT" {
		return Errorf(CodeInvalid, "this catalog uses KZT only")
	}
	return nil
}

func validateAttrs(a map[string]string) error {
	if len(a) > MaxAttributes {
		return Errorf(CodeInvalid, "more than %d attributes", MaxAttributes)
	}
	for k, v := range a {
		if k == "" || len(k) > 64 || len(v) > 256 {
			return Errorf(CodeInvalid, "attribute key must be 1..64 and value up to 256 bytes")
		}
	}
	return nil
}

func validateImages(keys []string) error {
	if len(keys) > MaxImageKeys {
		return Errorf(CodeInvalid, "more than %d image keys", MaxImageKeys)
	}
	for _, k := range keys {
		if k == "" || len(k) > 256 {
			return Errorf(CodeInvalid, "image key must be 1..256 bytes")
		}
	}
	return nil
}

// Normalize проверяет и дополняет параметры поиска значениями по умолчанию.
func (q *SearchQuery) Normalize() error {
	q.Q = strings.TrimSpace(q.Q)
	if utf8.RuneCountInString(q.Q) > MaxQueryLen {
		return Errorf(CodeInvalid, "q longer than %d characters", MaxQueryLen)
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit < 0 || q.Limit > MaxLimit {
		return Errorf(CodeInvalid, "limit must be 1..%d", MaxLimit)
	}
	if q.Sort == "" {
		if q.Q != "" {
			q.Sort = SortRelevance
		} else {
			q.Sort = SortNewest
		}
	}
	switch q.Sort {
	case SortRelevance:
		if q.Q == "" {
			return Errorf(CodeInvalid, "sort=relevance requires q")
		}
	case SortPriceAsc, SortPriceDesc, SortNewest:
	default:
		return Errorf(CodeInvalid, "unknown sort %q", q.Sort)
	}
	if q.CategoryID < 0 || q.BrandID < 0 {
		return Errorf(CodeInvalid, "category and brand must be positive")
	}
	if q.PriceMax != nil && *q.PriceMax < 0 {
		return Errorf(CodeInvalid, "price_max_minor must be >= 0")
	}
	if q.PriceMin != nil && *q.PriceMin < 0 {
		return Errorf(CodeInvalid, "price_min_minor must be >= 0")
	}
	if q.PriceMin != nil && q.PriceMax != nil && *q.PriceMin > *q.PriceMax {
		return Errorf(CodeInvalid, "price_min_minor greater than price_max_minor")
	}
	return nil
}
