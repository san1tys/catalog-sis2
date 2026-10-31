// Package app запускает два транспорта одного сервиса.
package app

import (
	"context"
	"errors"
	"github.com/adilbek/catalog-sis2/internal/service"
	"github.com/adilbek/catalog-sis2/internal/transport/grpcserver"
	"github.com/adilbek/catalog-sis2/internal/transport/httpserver"
	"google.golang.org/grpc"
	"net"
	"net/http"
	"time"
)

type App struct {
	http                       *http.Server
	grpc                       *grpc.Server
	httpListener, grpcListener net.Listener
	errors                     chan error
}

func Start(svc *service.Service, httpAddr, grpcAddr string, timeout time.Duration) (*App, error) {
	hl, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return nil, err
	}
	gl, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		hl.Close()
		return nil, err
	}
	a := &App{http: httpserver.NewServer(httpAddr, httpserver.New(svc, timeout)), grpc: grpcserver.New(svc, timeout), httpListener: hl, grpcListener: gl, errors: make(chan error, 2)}
	go func() {
		err := a.http.Serve(hl)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		a.errors <- err
	}()
	go func() { a.errors <- a.grpc.Serve(gl) }()
	return a, nil
}
func (a *App) HTTPAddr() string     { return a.httpListener.Addr().String() }
func (a *App) GRPCAddr() string     { return a.grpcListener.Addr().String() }
func (a *App) Errors() <-chan error { return a.errors }
func (a *App) Shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() { a.grpc.GracefulStop(); close(done) }()
	_ = a.http.Shutdown(ctx)
	select {
	case <-done:
	case <-ctx.Done():
		a.grpc.Stop()
	}
}
