package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adilbek/catalog-sis2/internal/app"
	"github.com/adilbek/catalog-sis2/internal/client"
	"github.com/adilbek/catalog-sis2/internal/client/grpcclient"
	"github.com/adilbek/catalog-sis2/internal/client/restclient"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/seedgen"
	"github.com/adilbek/catalog-sis2/internal/service"
	"github.com/adilbek/catalog-sis2/internal/store/memory"
	"github.com/adilbek/catalog-sis2/internal/transport/restconv"
)

var bg = context.Background()

func start(t *testing.T, store service.Store, timeout time.Duration) *app.App {
	t.Helper()
	a, err := app.Start(service.New(store), "127.0.0.1:0", "127.0.0.1:0", timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx, cancel := context.WithTimeout(bg, time.Second); defer cancel(); a.Shutdown(ctx) })
	return a
}
func connect(t *testing.T, a *app.App, protocol string) client.Catalog {
	t.Helper()
	var c client.Catalog
	if protocol == "rest" {
		c = restclient.New(restclient.Options{BaseURL: "http://" + a.HTTPAddr(), Actor: "test"})
	} else {
		var err error
		c, err = grpcclient.New(bg, grpcclient.Options{Addr: a.GRPCAddr(), Actor: "test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func input() domain.ProductInput {
	return domain.ProductInput{CategoryID: 2, BrandID: 1, Title: "New phone", PriceMinor: 12345, Currency: "KZT", InStock: true, Attributes: map[string]string{"color": "blue"}}
}
func wantCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	if domain.CodeOf(err) != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func TestProtocolParity(t *testing.T) {
	a := start(t, seedgen.NewStore(50), time.Second)
	rest := connect(t, a, "rest")
	rpc := connect(t, a, "grpc")
	p, err := rest.GetProduct(bg, 1)
	if err != nil {
		t.Fatal(err)
	}
	q, err := rpc.GetProduct(bg, 1)
	if err != nil || !reflect.DeepEqual(p, q) {
		t.Fatalf("product parity: %v %v %v", p, q, err)
	}
	cs, err := rest.ListCategories(bg)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := rpc.ListCategories(bg)
	if err != nil || !reflect.DeepEqual(cs, ds) {
		t.Fatalf("categories parity: %v", err)
	}
	query := domain.SearchQuery{Limit: 24, Sort: domain.SortPriceAsc}
	rs, err := rest.Search(bg, query)
	if err != nil {
		t.Fatal(err)
	}
	gs, err := rpc.Search(bg, query)
	// Cursor includes current expiry time. Page data must match; each cursor must work.
	if err != nil || !reflect.DeepEqual(rs.Items, gs.Items) || rs.NextCursor == "" || gs.NextCursor == "" {
		t.Fatalf("search parity: %v", err)
	}
	p, _, err = rest.CreateProduct(bg, "cross-protocol", input())
	if err != nil {
		t.Fatal(err)
	}
	q, replayed, err := rpc.CreateProduct(bg, "cross-protocol", input())
	if err != nil || !replayed || q.ID != p.ID {
		t.Fatalf("cross-protocol replay: %v %v", replayed, err)
	}
}
func TestCatalogContract(t *testing.T) {
	for _, protocol := range []string{"rest", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			m := seedgen.NewStore(50)
			c := connect(t, start(t, m, time.Second), protocol)
			p, replayed, err := c.CreateProduct(bg, "one", input())
			if err != nil || replayed {
				t.Fatalf("create: %v", err)
			}
			again, replayed, err := c.CreateProduct(bg, "one", input())
			if err != nil || !replayed || !reflect.DeepEqual(p, again) {
				t.Fatalf("replay: %v", err)
			}
			changed := input()
			changed.PriceMinor++
			_, _, err = c.CreateProduct(bg, "one", changed)
			wantCode(t, err, domain.CodeKeyReuse)
			_, _, err = c.CreateProduct(bg, "", input())
			wantCode(t, err, domain.CodeInvalid)
			p, err = c.UpdateProduct(bg, p.ID, p.Version, domain.ProductPatch{Attributes: map[string]string{}})
			if err != nil || len(p.Attributes) != 0 {
				t.Fatalf("clear attributes: %v %v", p, err)
			}
			title := "renamed"
			_, err = c.UpdateProduct(bg, p.ID, 1, domain.ProductPatch{Title: &title})
			wantCode(t, err, domain.CodeVersionConflict)
			_, err = c.HideProduct(bg, p.ID, p.Version)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.HideProduct(bg, p.ID, p.Version)
			if err != nil {
				t.Fatalf("repeat hide: %v", err)
			}
			_, err = c.GetProduct(bg, p.ID)
			wantCode(t, err, domain.CodeNotFound)
			hidden, err := c.GetProductAdmin(bg, p.ID)
			if err != nil || hidden.Status != domain.StatusHidden {
				t.Fatalf("admin read: %v", err)
			}
			seen := map[int64]bool{}
			query := domain.SearchQuery{Sort: domain.SortPriceAsc, Limit: 7}
			for {
				page, err := c.Search(bg, query)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Items {
					if seen[item.ID] {
						t.Fatalf("duplicate page item %d", item.ID)
					}
					seen[item.ID] = true
				}
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			if len(seen) != 50 {
				t.Fatalf("pagination: %d items", len(seen))
			}
			query.Cursor = "invalid"
			_, err = c.Search(bg, query)
			wantCode(t, err, domain.CodeInvalid)
			neg := int64(-1)
			_, err = c.Search(bg, domain.SearchQuery{PriceMax: &neg})
			wantCode(t, err, domain.CodeInvalid)
			usd := input()
			usd.Currency = "USD"
			_, _, err = c.CreateProduct(bg, "usd", usd)
			wantCode(t, err, domain.CodeInvalid)
			before := m.Len()
			var wg sync.WaitGroup
			ids := make(chan int64, 20)
			errs := make(chan error, 20)
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					p, _, err := c.CreateProduct(bg, "parallel", input())
					if err != nil {
						errs <- err
						return
					}
					ids <- p.ID
				}()
			}
			wg.Wait()
			close(ids)
			close(errs)
			for err := range errs {
				t.Error(err)
			}
			var id int64
			for got := range ids {
				if id != 0 && got != id {
					t.Errorf("different ids: %d and %d", id, got)
				}
				id = got
			}
			if m.Len() != before+1 {
				t.Fatalf("concurrent duplicate created %d products", m.Len()-before)
			}
		})
	}
}

func TestRESTDeadlineHeaderCarriesRequestID(t *testing.T) {
	a := start(t, seedgen.NewStore(24), time.Second)
	req, _ := http.NewRequest("GET", "http://"+a.HTTPAddr()+"/v1/products/1", nil)
	req.Header.Set(restconv.HeaderDeadlineMs, "invalid")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body restconv.Envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 400 || body.Error.Code != string(domain.CodeInvalid) {
		t.Fatalf("invalid deadline response: %d %+v", response.StatusCode, body)
	}
	if id := response.Header.Get(restconv.HeaderRequestID); id == "" || body.Error.RequestID != id {
		t.Fatalf("request id mismatch: header=%q body=%q", id, body.Error.RequestID)
	}
}

func TestRESTSlowBodyHonorsDeadline(t *testing.T) {
	for _, kind := range []string{"server-cap", "client-deadline"} {
		t.Run(kind, func(t *testing.T) {
			limit := 50 * time.Millisecond
			extraHeader := ""
			if kind == "client-deadline" {
				limit = time.Second
				extraHeader = "X-Request-Deadline-Ms: 50\r\n"
			}
			store := seedgen.NewStore(24)
			a := start(t, store, limit)
			conn, err := net.DialTimeout("tcp", a.HTTPAddr(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(input())
			began := time.Now()
			_, err = fmt.Fprintf(conn, "POST /v1/admin/products HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nIdempotency-Key: slow-body\r\nContent-Length: %d\r\n%s\r\n%s", len(body), extraHeader, body[:10])
			if err != nil {
				t.Fatal(err)
			}
			// Клиент не досылает тело. Ответ обязан прийти без помощи клиента.
			response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
			if err != nil {
				t.Fatalf("no response at body deadline: %v", err)
			}
			defer response.Body.Close()
			var envelope restconv.Envelope
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 504 || envelope.Error.Code != string(domain.CodeDeadline) {
				t.Fatalf("slow body response: status=%d %+v", response.StatusCode, envelope)
			}
			if time.Since(began) > 500*time.Millisecond {
				t.Fatalf("body deadline was not enforced: %s", time.Since(began))
			}
			if !response.Close {
				t.Fatal("incomplete body must close its connection")
			}
			if store.Len() != 24 {
				t.Fatal("partial request created a product")
			}
		})
	}
}

func TestRESTBodyDeadlineDoesNotLeakToKeepAlive(t *testing.T) {
	a := start(t, seedgen.NewStore(24), time.Second)
	conn, err := net.DialTimeout("tcp", a.HTTPAddr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	body, _ := json.Marshal(input())
	_, err = fmt.Fprintf(conn, "POST /v1/admin/products HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nIdempotency-Key: complete-body\r\nX-Request-Deadline-Ms: 50\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	if err != nil {
		t.Fatal(err)
	}
	first, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if err != nil || first.StatusCode != 201 || first.Close {
		t.Fatalf("complete request: status=%d close=%v err=%v", first.StatusCode, first.Close, err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err = fmt.Fprint(conn, "GET /v1/products/1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	second, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("keep-alive request after old deadline: %v", err)
	}
	defer second.Body.Close()
	if second.StatusCode != 200 {
		t.Fatalf("next request status: %d", second.StatusCode)
	}
}

// Failure happens after the store has committed its in-memory result, without a debug API.
type failingStore struct {
	*memory.Store
	before, after, calls, updates atomic.Int32
}

func consume(n *atomic.Int32) bool {
	for {
		old := n.Load()
		if old <= 0 {
			return false
		}
		if n.CompareAndSwap(old, old-1) {
			return true
		}
	}
}
func (s *failingStore) CreateIdempotent(ctx context.Context, key domain.IdemKey, in domain.ProductInput) (*domain.Product, bool, error) {
	s.calls.Add(1)
	if consume(&s.before) {
		return nil, false, domain.ErrUnavailable
	}
	p, replayed, err := s.Store.CreateIdempotent(ctx, key, in)
	if err == nil && consume(&s.after) {
		return nil, false, domain.ErrUnavailable
	}
	return p, replayed, err
}
func (s *failingStore) UpdateProduct(ctx context.Context, id, version int64, patch domain.ProductPatch) (*domain.Product, error) {
	s.updates.Add(1)
	_, err := s.Store.UpdateProduct(ctx, id, version, patch)
	if err != nil {
		return nil, err
	}
	return nil, domain.ErrUnavailable
}
func TestRetries(t *testing.T) {
	for _, protocol := range []string{"rest", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			s := &failingStore{Store: seedgen.NewStore(50)}
			c := connect(t, start(t, s, time.Second), protocol)
			r := client.NewResilient(c)
			r.BaseDelay = time.Millisecond
			r.MaxDelay = 2 * time.Millisecond
			s.after.Store(1)
			p, replayed, err := r.CreateProduct(bg, "lost", input())
			if err != nil || !replayed || s.Len() != 51 || s.calls.Load() != 2 {
				t.Fatalf("lost response retry: %v replayed=%v calls=%d products=%d", err, replayed, s.calls.Load(), s.Len())
			}
			s.calls.Store(0)
			s.before.Store(100)
			_, _, err = r.CreateProduct(bg, "bounded", input())
			wantCode(t, err, domain.CodeUnavailable)
			if s.calls.Load() != 3 || s.Len() != 51 {
				t.Fatalf("unbounded retries: %d", s.calls.Load())
			}
			s.before.Store(0)
			s.calls.Store(0)
			changed := input()
			changed.Title = "different"
			_, _, err = r.CreateProduct(bg, "lost", changed)
			wantCode(t, err, domain.CodeKeyReuse)
			if s.calls.Load() != 1 {
				t.Fatal("client error was retried")
			}
			title := "written once"
			_, err = r.UpdateProduct(bg, p.ID, p.Version, domain.ProductPatch{Title: &title})
			wantCode(t, err, domain.CodeUnavailable)
			got, err := c.GetProductAdmin(bg, p.ID)
			if err != nil || got.Title != title || s.updates.Load() != 1 {
				t.Fatalf("unknown update outcome: %v, attempts %d", err, s.updates.Load())
			}
		})
	}
}

type waitingStore struct {
	*memory.Store
	started, stopped chan struct{}
	deadline         chan time.Duration
}

func (s *waitingStore) GetProduct(ctx context.Context, id int64, hidden bool) (*domain.Product, error) {
	if dl, ok := ctx.Deadline(); ok {
		s.deadline <- time.Until(dl)
	}
	close(s.started)
	<-ctx.Done()
	close(s.stopped)
	return nil, ctx.Err()
}
func TestDeadlineAndCancellationReachStore(t *testing.T) {
	for _, protocol := range []string{"rest", "grpc"} {
		for _, kind := range []string{"deadline", "cancel", "server-cap"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				s := &waitingStore{Store: seedgen.NewStore(50), started: make(chan struct{}), stopped: make(chan struct{}), deadline: make(chan time.Duration, 1)}
				cap := time.Second
				if kind == "server-cap" {
					cap = 40 * time.Millisecond
				}
				c := connect(t, start(t, s, cap), protocol)
				var ctx context.Context
				var cancel context.CancelFunc
				if kind == "deadline" {
					ctx, cancel = context.WithTimeout(bg, 50*time.Millisecond)
				} else {
					ctx, cancel = context.WithCancel(bg)
				}
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := c.GetProduct(ctx, 1); done <- err }()
				select {
				case <-s.started:
				case <-time.After(time.Second):
					t.Fatal("store was not called")
				}
				if kind == "cancel" {
					cancel()
				}
				select {
				case err := <-done:
					code := domain.CodeDeadline
					if kind == "cancel" {
						code = domain.CodeCanceled
					}
					wantCode(t, err, code)
				case <-time.After(time.Second):
					t.Fatal("request did not stop")
				}
				select {
				case <-s.stopped:
				case <-time.After(300 * time.Millisecond):
					t.Fatal("store work survived cancellation")
				}
				if kind != "cancel" {
					select {
					case left := <-s.deadline:
						if left > 70*time.Millisecond {
							t.Fatalf("deadline not propagated: %s", left)
						}
					default:
						t.Fatal("store received no deadline")
					}
				}
			})
		}
	}
}
