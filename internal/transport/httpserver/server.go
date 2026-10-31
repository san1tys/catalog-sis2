// Package httpserver реализует baseline REST/JSON по HTTP/1.1.
package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/service"
	"github.com/adilbek/catalog-sis2/internal/transport/restconv"
)

type api struct{ svc *service.Service }
type ctxKey int

const requestIDKey ctxKey = 1

func New(svc *service.Service, timeout time.Duration) http.Handler {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	a := &api{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/products/{id}", a.wrap(a.getProduct))
	mux.HandleFunc("GET /v1/admin/products/{id}", a.wrap(a.getProductAdmin))
	mux.HandleFunc("GET /v1/categories", a.wrap(a.listCategories))
	mux.HandleFunc("GET /v1/search", a.wrap(a.search))
	mux.HandleFunc("POST /v1/admin/products", a.wrap(a.createProduct))
	mux.HandleFunc("PATCH /v1/admin/products/{id}", a.wrap(a.updateProduct))
	mux.HandleFunc("DELETE /v1/admin/products/{id}", a.wrap(a.hideProduct))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [8]byte
		_, _ = rand.Read(id[:])
		rid := hex.EncodeToString(id[:])
		w.Header().Set(restconv.HeaderRequestID, rid)
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		r = r.WithContext(ctx)
		budget := timeout
		if raw := r.Header.Get(restconv.HeaderDeadlineMs); raw != "" {
			ms, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || ms < 0 {
				a.writeError(w, r, domain.Errorf(domain.CodeInvalid, "invalid deadline header"))
				return
			}
			// Сначала ограничиваем число миллисекунд, затем переводим в Duration: без overflow.
			if ms < budget.Milliseconds() {
				budget = time.Duration(ms) * time.Millisecond
			}
		}
		ctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		r = r.WithContext(ctx)
		if err := ctx.Err(); err != nil {
			a.writeError(w, r, err)
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			// Контекст сам по себе не прерывает Read тела HTTP/1.1.
			controller := http.NewResponseController(w)
			deadline, _ := ctx.Deadline()
			if err := controller.SetReadDeadline(deadline); err != nil {
				a.writeError(w, r, domain.Errorf(domain.CodeInternal, "cannot set request body deadline"))
				return
			}
			defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
		}
		mux.ServeHTTP(w, r)
	})
}
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 1 << 16}
}

type handlerFunc func(http.ResponseWriter, *http.Request) error

func (a *api) wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			a.writeError(w, r, err)
		}
	}
}
func (a *api) writeError(w http.ResponseWriter, r *http.Request, err error) {
	rid, _ := r.Context().Value(requestIDKey).(string)
	writeJSON(w, restconv.Status(domain.CodeOf(err)), restconv.Envelope{Error: restconv.ErrorBody{Code: string(domain.CodeOf(err)), Message: domain.MessageOf(err), RequestID: rid}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// parseIfMatch принимает сильную версию "7" или 7.
func parseIfMatch(r *http.Request) (int64, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if strings.HasPrefix(v, "W/") {
		return 0, domain.Errorf(domain.CodeInvalid, "If-Match requires a strong version ETag")
	}
	v = strings.Trim(v, `"`)
	if v == "" {
		return 0, domain.Errorf(domain.CodeInvalid, "If-Match header with the current version is required")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, domain.Errorf(domain.CodeInvalid, "If-Match must contain a positive version")
	}
	return n, nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Errorf(domain.CodeInvalid, "id must be a positive integer")
	}
	return id, nil
}

func (a *api) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if timed := bodyReadError(w, r, err); timed != nil {
			return timed
		}
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			return domain.Errorf(domain.CodeInvalid, "request body too large")
		case errors.Is(err, io.EOF):
			return domain.Errorf(domain.CodeInvalid, "request body is required")
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return err
		}
		return domain.Errorf(domain.CodeInvalid, "invalid JSON: %v", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		if timed := bodyReadError(w, r, err); timed != nil {
			return timed
		}
		return domain.Errorf(domain.CodeInvalid, "unexpected data after JSON body")
	}
	return nil
}

func bodyReadError(w http.ResponseWriter, r *http.Request, err error) error {
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		// После неполного тела соединение закрывается: его остаток не станет новым запросом.
		w.Header().Set("Connection", "close")
		return domain.ErrDeadline
	}
	if canceled := r.Context().Err(); canceled != nil {
		w.Header().Set("Connection", "close")
		return canceled
	}
	return nil
}

// ---- обработчики ------------------------------------------------------------

func (a *api) getProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p, err := a.svc.GetProduct(r.Context(), id)
	if err != nil {
		return err
	}
	tag := etag(p.Version)
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}

func (a *api) getProductAdmin(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p, err := a.svc.GetProductAdmin(r.Context(), id)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", etag(p.Version))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, p)
	return nil
}

func (a *api) listCategories(w http.ResponseWriter, r *http.Request) error {
	l, err := a.svc.ListCategories(r.Context())
	if err != nil {
		return err
	}
	w.Header().Set("ETag", l.ETag)
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("If-None-Match") == l.ETag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	writeJSON(w, http.StatusOK, l)
	return nil
}

func queryInt(r *http.Request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, domain.Errorf(domain.CodeInvalid, "%s must be an integer", name)
	}
	return n, nil
}

func queryOptInt(r *http.Request, name string) (*int64, error) {
	if !r.URL.Query().Has(name) {
		return nil, nil
	}
	n, err := queryInt(r, name)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (a *api) search(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	q := domain.SearchQuery{Q: qs.Get("q"), Sort: qs.Get("sort"), Cursor: qs.Get("cursor")}
	var err error
	if q.CategoryID, err = queryInt(r, "category"); err != nil {
		return err
	}
	if q.BrandID, err = queryInt(r, "brand"); err != nil {
		return err
	}
	if q.PriceMin, err = queryOptInt(r, "price_min_minor"); err != nil {
		return err
	}
	if q.PriceMax, err = queryOptInt(r, "price_max_minor"); err != nil {
		return err
	}
	lim, err := queryInt(r, "limit")
	if err != nil {
		return err
	}
	q.Limit = int(lim)
	res, err := a.svc.Search(r.Context(), q)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (a *api) createProduct(w http.ResponseWriter, r *http.Request) error {
	var in domain.ProductInput
	if err := a.decodeBody(w, r, &in); err != nil {
		return err
	}
	p, replayed, err := a.svc.CreateProduct(r.Context(), r.Header.Get(restconv.HeaderActor), r.Header.Get(restconv.HeaderIdempotencyKey), in)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("ETag", etag(p.Version))
	h.Set("Location", "/v1/admin/products/"+strconv.FormatInt(p.ID, 10))
	status := http.StatusCreated
	if replayed {
		h.Set(restconv.HeaderReplayed, "true")
		status = http.StatusOK
	}
	writeJSON(w, status, p)
	return nil
}

func (a *api) updateProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ver, err := parseIfMatch(r)
	if err != nil {
		return err
	}
	var patch domain.ProductPatch
	if err := a.decodeBody(w, r, &patch); err != nil {
		return err
	}
	p, err := a.svc.UpdateProduct(r.Context(), id, ver, patch)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", etag(p.Version))
	writeJSON(w, http.StatusOK, p)
	return nil
}

func (a *api) hideProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ver, err := parseIfMatch(r)
	if err != nil {
		return err
	}
	nv, err := a.svc.HideProduct(r.Context(), id, ver)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", etag(nv))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
