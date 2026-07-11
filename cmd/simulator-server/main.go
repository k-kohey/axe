package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/k-kohey/axe/internal/simruntime"
	"github.com/k-kohey/axe/internal/simulatorserver"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/grpc"
)

var version = "dev"

func main() {
	var (
		httpAddr      = flag.String("http", "127.0.0.1:3977", "HTTP listen address")
		grpcAddr      = flag.String("grpc", "127.0.0.1:3978", "gRPC listen address")
		deviceSetPath = flag.String("device-set", "", "CoreSimulator device set path")
		verbose       = flag.Bool("verbose", false, "verbose logging")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	opts := []simruntime.Option{}
	if *deviceSetPath != "" {
		opts = append(opts, simruntime.WithDeviceSetPath(*deviceSetPath))
	}
	runtime, err := simruntime.New(opts...)
	if err != nil {
		slog.Error("failed to initialize runtime", "err", err)
		os.Exit(1)
	}

	grpcListener, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		slog.Error("failed to listen for gRPC", "addr", *grpcAddr, "err", err)
		os.Exit(1)
	}
	grpcServer := grpc.NewServer()
	simulatorv1.RegisterSimulatorServiceServer(grpcServer, simulatorserver.NewGRPCServer(runtime, version))

	httpServer := &http.Server{
		Addr:              *httpAddr,
		Handler:           simulatorserver.NewHTTPServer(runtime, version).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		slog.Info("simulator-server gRPC listening", "addr", *grpcAddr)
		if err := grpcServer.Serve(grpcListener); err != nil {
			slog.Error("gRPC server stopped", "err", err)
			stop()
		}
	}()
	go func() {
		defer wg.Done()
		slog.Info("simulator-server HTTP listening", "addr", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server stopped", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	grpcServer.GracefulStop()
	runtime.Shutdown(shutdownCtx)
	wg.Wait()
}
