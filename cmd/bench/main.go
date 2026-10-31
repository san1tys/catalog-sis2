// Команда bench выполняет последовательный latency microbenchmark без генератора нагрузки.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	pb "github.com/adilbek/catalog-sis2/gen/catalog/v1"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/transport/pbconv"
	"github.com/adilbek/catalog-sis2/internal/transport/restconv"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type operation func(context.Context) (int, error)
type observation struct {
	repeat         int
	protocol, name string
	sample         int
	latency        time.Duration
	bytes          int
	code           domain.Code
}
type summary struct {
	repeat, protocol, name  string
	count, errors, timeouts int
	p50, p95, p99           float64
	bytes                   float64
}

func fatal(err error)    { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func f(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
func quantile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(values)))) - 1
	if i < 0 {
		i = 0
	}
	return values[i]
}
func aggregate(repeat, protocol, name string, rows []observation) summary {
	s := summary{repeat: repeat, protocol: protocol, name: name}
	var values []float64
	var body int
	for _, r := range rows {
		if r.protocol != protocol || r.name != name || repeat != "all" && strconv.Itoa(r.repeat) != repeat {
			continue
		}
		s.count++
		values = append(values, float64(r.latency)/float64(time.Millisecond))
		if r.code != "" {
			s.errors++
		}
		if r.code == domain.CodeDeadline {
			s.timeouts++
		}
		if r.code == "" {
			body += r.bytes
		}
	}
	sort.Float64s(values)
	s.p50 = quantile(values, .5)
	s.p95 = quantile(values, .95)
	s.p99 = quantile(values, .99)
	if s.count > s.errors {
		s.bytes = float64(body) / float64(s.count-s.errors)
	}
	return s
}
func writeCSV(path string, header []string, rows [][]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
func restOperation(hc *http.Client, base, path, name string) operation {
	return func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if err != nil {
			return 0, err
		}
		if dl, ok := ctx.Deadline(); ok {
			ms := time.Until(dl).Milliseconds()
			if ms < 1 {
				ms = 1
			}
			req.Header.Set(restconv.HeaderDeadlineMs, strconv.FormatInt(ms, 10))
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, domain.ErrUnavailable
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, err
		}
		if resp.StatusCode != 200 {
			return 0, &domain.Error{Code: restconv.CodeFromStatus(resp.StatusCode), Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}
		// Сериализация сервера и десериализация клиента входят в latency обоих протоколов.
		switch name {
		case "get":
			var p domain.Product
			err = json.Unmarshal(b, &p)
		case "search":
			var p domain.SearchResult
			err = json.Unmarshal(b, &p)
		case "categories":
			var p domain.CategoryList
			err = json.Unmarshal(b, &p)
		}
		return len(b), err
	}
}
func main() {
	httpAddr := flag.String("http-addr", "127.0.0.1:8080", "REST address")
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:50051", "gRPC address")
	warmup := flag.Int("warmup", 100, "warm-up requests per operation/protocol/repeat")
	samples := flag.Int("samples", 1000, "measured requests per operation/protocol/repeat")
	repeats := flag.Int("repeats", 3, "repeats with alternating protocol order")
	timeout := flag.Duration("timeout", 80*time.Millisecond, "deadline for each measured read")
	out := flag.String("out", "bench/results", "output directory (files are overwritten)")
	flag.Parse()
	if *warmup < 0 || *samples < 1 || *repeats < 1 || *timeout <= 0 {
		fatal(fmt.Errorf("warmup >= 0, samples/repeats > 0 and timeout > 0 required"))
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fatal(err)
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	conn, err := grpc.DialContext(dialCtx, *grpcAddr, grpc.WithInsecure(), grpc.WithBlock())
	cancel()
	if err != nil {
		fatal(err)
	}
	defer conn.Close()
	rpc := pb.NewCatalogServiceClient(conn)
	tr := &http.Transport{DisableCompression: true, MaxIdleConnsPerHost: 1}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}
	ops := map[string]map[string]operation{
		"rest": {
			"get":        restOperation(hc, "http://"+*httpAddr, "/v1/products/1", "get"),
			"search":     restOperation(hc, "http://"+*httpAddr, "/v1/search?sort=price_asc&limit=24", "search"),
			"categories": restOperation(hc, "http://"+*httpAddr, "/v1/categories", "categories"),
		},
		"grpc": {
			"get": func(ctx context.Context) (int, error) {
				p, err := rpc.GetProduct(ctx, &pb.GetProductRequest{Id: 1})
				if err != nil {
					return 0, pbconv.FromStatusError(err, nil)
				}
				return proto.Size(p), nil
			},
			"search": func(ctx context.Context) (int, error) {
				p, err := rpc.Search(ctx, &pb.SearchRequest{Sort: domain.SortPriceAsc, Limit: 24})
				if err != nil {
					return 0, pbconv.FromStatusError(err, nil)
				}
				return proto.Size(p), nil
			},
			"categories": func(ctx context.Context) (int, error) {
				p, err := rpc.ListCategories(ctx, &pb.ListCategoriesRequest{})
				if err != nil {
					return 0, pbconv.FromStatusError(err, nil)
				}
				return proto.Size(p), nil
			},
		},
	}
	var rows []observation
	started := time.Now().UTC()
	names := []string{"get", "search", "categories"}
	for repeat := 1; repeat <= *repeats; repeat++ {
		protocols := []string{"rest", "grpc"}
		if repeat%2 == 0 {
			protocols = []string{"grpc", "rest"}
		}
		for _, name := range names {
			for _, protocol := range protocols {
				op := ops[protocol][name]
				for i := 0; i < *warmup; i++ {
					ctx, cancel := context.WithTimeout(context.Background(), *timeout)
					_, err := op(ctx)
					cancel()
					if err != nil {
						fatal(fmt.Errorf("warmup %s/%s: %w", protocol, name, err))
					}
				}
				for i := 1; i <= *samples; i++ {
					ctx, cancel := context.WithTimeout(context.Background(), *timeout)
					start := time.Now()
					body, err := op(ctx)
					latency := time.Since(start)
					cancel()
					rows = append(rows, observation{repeat: repeat, protocol: protocol, name: name, sample: i, latency: latency, bytes: body, code: domain.CodeOf(err)})
				}
				fmt.Printf("repeat %d: %-4s %-10s %d requests\n", repeat, protocol, name, *samples)
			}
		}
	}
	var raw [][]string
	for _, r := range rows {
		raw = append(raw, []string{strconv.Itoa(r.repeat), r.protocol, r.name, strconv.Itoa(r.sample), strconv.FormatInt(r.latency.Nanoseconds(), 10), strconv.Itoa(r.bytes), string(r.code)})
	}
	if err := writeCSV(filepath.Join(*out, "raw.csv"), []string{"repeat", "protocol", "operation", "sample", "latency_ns", "response_body_bytes", "error_code"}, raw); err != nil {
		fatal(err)
	}
	var summaries []summary
	var table [][]string
	for repeat := 1; repeat <= *repeats+1; repeat++ {
		label := strconv.Itoa(repeat)
		if repeat > *repeats {
			label = "all"
		}
		for _, name := range names {
			for _, protocol := range []string{"rest", "grpc"} {
				s := aggregate(label, protocol, name, rows)
				summaries = append(summaries, s)
				table = append(table, []string{s.repeat, s.protocol, s.name, strconv.Itoa(s.count), strconv.Itoa(s.count - s.errors), strconv.Itoa(s.errors), strconv.Itoa(s.timeouts), f(s.p50), f(s.p95), f(s.p99), f(s.bytes)})
			}
		}
	}
	if err := writeCSV(filepath.Join(*out, "summary.csv"), []string{"repeat", "protocol", "operation", "requests", "successes", "errors", "timeouts", "p50_ms", "p95_ms", "p99_ms", "mean_response_body_bytes"}, table); err != nil {
		fatal(err)
	}
	environment := map[string]any{"started_utc": started.Format(time.RFC3339), "finished_utc": time.Now().UTC().Format(time.RFC3339), "client_go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "http_addr": *httpAddr, "grpc_addr": *grpcAddr, "warmup_per_group": *warmup, "samples_per_group": *samples, "repeats": *repeats, "timeout": timeout.String(), "mode": "sequential, one outstanding read, warm connections, no retries or compression"}
	b, _ := json.MarshalIndent(environment, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "environment.json"), append(b, '\n'), 0644); err != nil {
		fatal(err)
	}
	report := "# Измерения SIS 2\n\nФактический запуск " + started.Format(time.RFC3339) + ". Последовательный microbenchmark: один запрос в полёте, прогретые соединения, без повторов и сжатия. Порядок протоколов меняется между повторами.\n\n"
	report += fmt.Sprintf("Клиент: %s, %s/%s, GOMAXPROCS=%d. Прогрев %d, измерений %d на группу, повторов %d, timeout %s. Размер каталога задаётся сервером (`-products`, по умолчанию 2000); benchmark его не меняет.\n\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), *warmup, *samples, *repeats, timeout.String())
	report += "| Операция | Протокол | Успехи | Ошибки | Timeouts | p50 мс | p95 мс | p99 мс | Тело ответа, байт |\n|---|---|---:|---:|---:|---:|---:|---:|---:|\n"
	errors := 0
	for _, s := range summaries {
		if s.repeat != "all" {
			continue
		}
		errors += s.errors
		report += fmt.Sprintf("| %s | %s | %d | %d | %d | %.3f | %.3f | %.3f | %.1f |\n", s.name, s.protocol, s.count-s.errors, s.errors, s.timeouts, s.p50, s.p95, s.p99, s.bytes)
	}
	report += "\nКвантили объединяют все измеренные попытки всех повторов, включая ошибки и таймауты. Метод nearest rank. Средний размер тела считается только для успешных ответов: фактическая длина JSON body и `proto.Size` полученного protobuf-сообщения. Заголовки HTTP, фреймы HTTP/2/gRPC, TCP/IP и TLS сюда не входят. Дополнительный вызов `proto.Size` входит в измеряемый gRPC-код; это небольшая измерительная накладная стоимость.\n\nЭто сравнение реализаций протоколов на данном стенде, а не нагрузочный тест или доказательство SLO из SIS #1. CPU и сеть клиента, сервера и устройства влияют на результаты. Между повторными запусками значения могут меняться. `raw.csv` содержит каждую попытку, `summary.csv` — каждый повтор и общий итог, `environment.json` — параметры. Команда заменяет файлы, поэтому для сохранения предыдущего запуска выбирайте другой `-out`.\n"
	if err := os.WriteFile(filepath.Join(*out, "REPORT.md"), []byte(report), 0644); err != nil {
		fatal(err)
	}
	fmt.Printf("Results: %s (errors=%d)\n", *out, errors)
	if errors > 0 {
		os.Exit(1)
	}
}
