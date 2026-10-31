// Package restclient — клиент Catalog API поверх REST/JSON.
// HTTP/1.1 baseline для сравнения с gRPC.
package restclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/idempotency"
	"github.com/adilbek/catalog-sis2/internal/transport/restconv"
)

type Options struct {
	BaseURL string
	Actor   string
}

type Client struct {
	hc    *http.Client
	tr    *http.Transport
	base  string
	actor string
}

func New(o Options) *Client {
	if o.Actor == "" {
		o.Actor = idempotency.AnonymousActor
	}
	tr := &http.Transport{MaxIdleConns: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: time.Minute, DisableCompression: true}
	return &Client{hc: &http.Client{Transport: tr}, tr: tr, base: o.BaseURL, actor: o.Actor}
}

func (c *Client) Close() error {
	c.tr.CloseIdleConnections()
	return nil
}

// do выполняет запрос и возвращает статус, заголовки и тело; ошибки сети и
// статусы >= 400 превращает в доменные ошибки.
func (c *Client) do(ctx context.Context, method, path string, hdr map[string]string, body any) (int, http.Header, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, domain.Errorf(domain.CodeInvalid, "encode body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, nil, domain.Errorf(domain.CodeInvalid, "build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(restconv.HeaderActor, c.actor)
	// остаток бюджета времени — серверу (аналог grpc-timeout)
	if dl, ok := ctx.Deadline(); ok {
		ms := time.Until(dl).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		req.Header.Set(restconv.HeaderDeadlineMs, strconv.FormatInt(ms, 10))
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, nil, nil, &domain.Error{Code: domain.CodeDeadline, Message: err.Error()}
		case errors.Is(err, context.Canceled):
			return 0, nil, nil, &domain.Error{Code: domain.CodeCanceled, Message: err.Error()}
		}
		return 0, nil, nil, &domain.Error{Code: domain.CodeUnavailable, Message: err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, nil, nil, &domain.Error{Code: domain.CodeDeadline, Message: err.Error()}
		}
		return 0, nil, nil, &domain.Error{Code: domain.CodeUnavailable, Message: err.Error()}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, resp.Header, data, decodeError(resp.StatusCode, resp.Header, data)
	}
	return resp.StatusCode, resp.Header, data, nil
}

func decodeError(status int, h http.Header, data []byte) error {
	var env restconv.Envelope
	if json.Unmarshal(data, &env) == nil && env.Error.Code != "" {
		return &domain.Error{Code: domain.Code(env.Error.Code), Message: env.Error.Message, RequestID: env.Error.RequestID}
	}
	return &domain.Error{Code: restconv.CodeFromStatus(status), Message: fmt.Sprintf("HTTP %d", status), RequestID: h.Get(restconv.HeaderRequestID)}
}

func decode[T any](data []byte) (*T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, &domain.Error{Code: domain.CodeInternal, Message: "decode response: " + err.Error()}
	}
	return &v, nil
}

func (c *Client) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	_, _, data, err := c.do(ctx, http.MethodGet, "/v1/products/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil {
		return nil, err
	}
	return decode[domain.Product](data)
}

func (c *Client) GetProductAdmin(ctx context.Context, id int64) (*domain.Product, error) {
	_, _, data, err := c.do(ctx, http.MethodGet, "/v1/admin/products/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil {
		return nil, err
	}
	return decode[domain.Product](data)
}

func (c *Client) ListCategories(ctx context.Context) (*domain.CategoryList, error) {
	_, h, data, err := c.do(ctx, http.MethodGet, "/v1/categories", nil, nil)
	if err != nil {
		return nil, err
	}
	l, err := decode[domain.CategoryList](data)
	if err != nil {
		return nil, err
	}
	l.ETag = h.Get("ETag")
	return l, nil
}

func (c *Client) Search(ctx context.Context, q domain.SearchQuery) (*domain.SearchResult, error) {
	v := url.Values{}
	if q.Q != "" {
		v.Set("q", q.Q)
	}
	if q.CategoryID != 0 {
		v.Set("category", strconv.FormatInt(q.CategoryID, 10))
	}
	if q.BrandID != 0 {
		v.Set("brand", strconv.FormatInt(q.BrandID, 10))
	}
	if q.PriceMin != nil {
		v.Set("price_min_minor", strconv.FormatInt(*q.PriceMin, 10))
	}
	if q.PriceMax != nil {
		v.Set("price_max_minor", strconv.FormatInt(*q.PriceMax, 10))
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.Limit != 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	path := "/v1/search"
	if enc := v.Encode(); enc != "" {
		path += "?" + enc
	}
	_, _, data, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	return decode[domain.SearchResult](data)
}

func (c *Client) CreateProduct(ctx context.Context, key string, in domain.ProductInput) (*domain.Product, bool, error) {
	_, h, data, err := c.do(ctx, http.MethodPost, "/v1/admin/products", map[string]string{restconv.HeaderIdempotencyKey: key}, in)
	if err != nil {
		return nil, false, err
	}
	p, err := decode[domain.Product](data)
	if err != nil {
		return nil, false, err
	}
	return p, h.Get(restconv.HeaderReplayed) == "true", nil
}

func (c *Client) UpdateProduct(ctx context.Context, id, expectedVersion int64, patch domain.ProductPatch) (*domain.Product, error) {
	_, _, data, err := c.do(ctx, http.MethodPatch, "/v1/admin/products/"+strconv.FormatInt(id, 10),
		map[string]string{"If-Match": `"` + strconv.FormatInt(expectedVersion, 10) + `"`}, patch)
	if err != nil {
		return nil, err
	}
	return decode[domain.Product](data)
}

func (c *Client) HideProduct(ctx context.Context, id, expectedVersion int64) (int64, error) {
	_, h, _, err := c.do(ctx, http.MethodDelete, "/v1/admin/products/"+strconv.FormatInt(id, 10),
		map[string]string{"If-Match": `"` + strconv.FormatInt(expectedVersion, 10) + `"`}, nil)
	if err != nil {
		return 0, err
	}
	v, _ := strconv.ParseInt(trimQuotes(h.Get("ETag")), 10, 64)
	return v, nil
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
