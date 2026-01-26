package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadConfig()

	sliceAddr := "localhost" + cfg.GetSliceServiceAddr()
	adminAddr := "localhost" + cfg.GetAdminServiceAddr()
	gatewayAddr := cfg.GetGatewayAddr()

	ctx := context.Background()
	gatewayMux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := filev1.RegisterFileServiceHandlerFromEndpoint(ctx, gatewayMux, sliceAddr, opts); err != nil {
		log.Fatalf("Failed to register file service gateway: %v", err)
	}
	if err := adminv1.RegisterAdminServiceHandlerFromEndpoint(ctx, gatewayMux, adminAddr, opts); err != nil {
		log.Fatalf("Failed to register admin service gateway: %v", err)
	}

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("gateway-service"))
	httpMux.HandleFunc("/ready", common.ReadyCheckHandler("gateway-service", func(ctx context.Context) bool {
		return grpcReady(ctx, sliceAddr) && grpcReady(ctx, adminAddr)
	}))
	httpMux.Handle("/", withCORS(gatewayMux))

	server := &http.Server{
		Addr:    gatewayAddr,
		Handler: httpMux,
	}

	log.Printf("Gateway listening on %s", gatewayAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve gateway: %v", err)
	}
}

func grpcReady(ctx context.Context, addr string) bool {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return false
	}
	return conn.Close() == nil
}

func withCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		handler.ServeHTTP(w, r)
	})
}
