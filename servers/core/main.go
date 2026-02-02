package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/gateway"
	"github.com/niczy/gitslice/internal/storage"
	adminservice "github.com/niczy/gitslice/services/admin"
	fileservice "github.com/niczy/gitslice/services/file"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.LoadConfig()

	st := storage.NewInMemoryStorage()
	ctx := context.Background()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		log.Fatalf("Failed to initialize root slice: %v", err)
	}
	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		log.Printf("Warning: genesis population failed: %v", err)
	}

	grpcAddr := cfg.GetCoreServiceAddr()
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen on gRPC addr %s: %v", grpcAddr, err)
	}

	grpcServer := grpc.NewServer()
	sliceservice.RegisterGRPCServer(grpcServer, st)
	fileservice.RegisterGRPCServer(grpcServer, st)
	adminservice.RegisterGRPCServer(grpcServer, st)

	go func() {
		log.Printf("Core gRPC server listening on %s", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	grpcDialAddr := "localhost" + grpcAddr
	gatewayMux, closeConns, err := gateway.NewMux(ctx, grpcDialAddr)
	if err != nil {
		log.Fatalf("Failed to create gateway mux: %v", err)
	}
	defer closeConns()

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("core-server"))
	httpMux.HandleFunc("/ready", common.ReadyCheckHandler("core-server", func(ctx context.Context) bool {
		return gateway.GRPCReady(ctx, grpcDialAddr)
	}))
	httpMux.Handle("/", gateway.WithCORS(gatewayMux))

	server := &http.Server{
		Addr:    cfg.GetGatewayAddr(),
		Handler: httpMux,
	}

	log.Printf("Gateway listening on %s", cfg.GetGatewayAddr())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve gateway: %v", err)
	}
}
