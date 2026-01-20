package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	fileservice "github.com/niczy/gitslice/internal/services/file"
	sliceservice "github.com/niczy/gitslice/internal/services/slice"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Load configuration
	cfg := config.LoadConfig()

	// Initialize storage
	st := storage.NewInMemoryStorage()

	// Initialize root slice
	ctx := context.Background()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		log.Fatalf("Failed to initialize root slice: %v", err)
	}

	grpcAddr := cfg.GetSliceServiceAddr()
	gatewayAddr := cfg.GetGatewayAddr()

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := sliceservice.NewGRPCServer(st)
	filev1.RegisterFileServiceServer(s, fileservice.NewService(st))

	go func() {
		log.Printf("SliceService server listening on %s", grpcAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	ctx := context.Background()
	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := filev1.RegisterFileServiceHandlerFromEndpoint(ctx, mux, "localhost"+grpcAddr, opts); err != nil {
		log.Fatalf("Failed to register file service gateway: %v", err)
	}

	// Create HTTP mux with health checks
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("slice-service"))
	httpMux.HandleFunc("/ready", common.ReadyCheckHandler("slice-service", func(ctx context.Context) bool {
		// Check if storage is accessible
		_, err := st.GetRootSlice(ctx)
		return err == nil
	}))
	httpMux.Handle("/", withCORS(mux))

	server := &http.Server{
		Addr:    gatewayAddr,
		Handler: httpMux,
	}

	log.Printf("FileService gateway listening on %s", gatewayAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve gateway: %v", err)
	}
}

func withCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		handler.ServeHTTP(w, r)
	})
}
