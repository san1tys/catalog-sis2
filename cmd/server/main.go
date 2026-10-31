package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/adilbek/catalog-sis2/internal/app"
	"github.com/adilbek/catalog-sis2/internal/seedgen"
	"github.com/adilbek/catalog-sis2/internal/service"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	httpAddr := flag.String("http-addr", "127.0.0.1:8080", "REST HTTP/1.1 address")
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:50051", "gRPC address")
	products := flag.Int("products", 2000, "initial product count")
	timeout := flag.Duration("timeout", 2*time.Second, "server request timeout ceiling")
	flag.Parse()
	if *products < 24 || *timeout <= 0 {
		log.Fatal("products must be >= 24; timeout must be positive")
	}
	a, err := app.Start(service.New(seedgen.NewStore(*products)), *httpAddr, *grpcAddr, *timeout)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Catalog: REST http://%s, gRPC %s; %d in-memory products\n", a.HTTPAddr(), a.GRPCAddr(), *products)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-a.Errors():
		log.Printf("server stopped: %v", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a.Shutdown(shutdown)
}
