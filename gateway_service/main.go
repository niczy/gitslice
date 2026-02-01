package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func main() {
	cfg := config.LoadConfig()

	sliceAddr := "localhost" + cfg.GetSliceServiceAddr()
	adminAddr := "localhost" + cfg.GetAdminServiceAddr()
	gatewayAddr := cfg.GetGatewayAddr()

	ctx := context.Background()
	gatewayMux, closeConns, err := newGatewayMux(ctx, sliceAddr, adminAddr)
	if err != nil {
		log.Fatalf("Failed to create gateway mux: %v", err)
	}
	defer closeConns()

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

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %v, shutting down gateway gracefully...", sig)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Gateway shutdown error: %v", err)
		}
	}()

	log.Printf("Gateway listening on %s", gatewayAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve gateway: %v", err)
	}
	log.Println("Gateway server stopped")
}

func newGatewayMux(ctx context.Context, sliceAddr, adminAddr string) (*runtime.ServeMux, func(), error) {
	gatewayMux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	sliceConn, err := grpc.DialContext(ctx, sliceAddr, opts...)
	if err != nil {
		return nil, func() {}, err
	}

	adminConn, err := grpc.DialContext(ctx, adminAddr, opts...)
	if err != nil {
		_ = sliceConn.Close()
		return nil, func() {}, err
	}

	fileClient := filev1.NewFileServiceClient(sliceConn)
	adminClient := adminv1.NewAdminServiceClient(adminConn)

	if err := filev1.RegisterFileServiceHandlerClient(ctx, gatewayMux, fileClient); err != nil {
		_ = sliceConn.Close()
		_ = adminConn.Close()
		return nil, func() {}, err
	}
	if err := adminv1.RegisterAdminServiceHandlerClient(ctx, gatewayMux, adminClient); err != nil {
		_ = sliceConn.Close()
		_ = adminConn.Close()
		return nil, func() {}, err
	}

	if err := registerFileEntriesOverrides(gatewayMux, fileClient); err != nil {
		_ = sliceConn.Close()
		_ = adminConn.Close()
		return nil, func() {}, err
	}

	closeConns := func() {
		_ = sliceConn.Close()
		_ = adminConn.Close()
	}
	return gatewayMux, closeConns, nil
}

func registerFileEntriesOverrides(mux *runtime.ServeMux, client filev1.FileServiceClient) error {
	if err := mux.HandlePath(http.MethodGet, "/v1/files/entries", func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		handleListEntries(w, r, mux, client, pathParams, "/v1/files/entries")
	}); err != nil {
		return err
	}
	return mux.HandlePath(http.MethodGet, "/v1/files/entries/{path=**}", func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		handleListEntries(w, r, mux, client, pathParams, "/v1/files/entries/{path=**}")
	})
}

func handleListEntries(
	w http.ResponseWriter,
	r *http.Request,
	mux *runtime.ServeMux,
	client filev1.FileServiceClient,
	pathParams map[string]string,
	pattern string,
) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	_, outboundMarshaler := runtime.MarshalerForRequest(mux, r)
	annotatedContext, err := runtime.AnnotateContext(ctx, mux, r, "/file.v1.FileService/ListEntries", runtime.WithHTTPPathPattern(pattern))
	if err != nil {
		runtime.HTTPError(ctx, mux, outboundMarshaler, w, r, err)
		return
	}

	req := &filev1.ListEntriesRequest{}
	if pathValue := pathParams["path"]; pathValue != "" {
		req.Path = pathValue
	}

	query := r.URL.Query()
	if req.Path == "" {
		req.Path = query.Get("path")
	}

	if limitValue := query.Get("limit"); limitValue != "" {
		limit, err := strconv.Atoi(limitValue)
		if err != nil {
			runtime.HTTPError(annotatedContext, mux, outboundMarshaler, w, r, status.Error(codes.InvalidArgument, "limit must be an integer"))
			return
		}
		req.Limit = int32(limit)
	}

	if sliceID := query.Get("slice_version.slice_id"); sliceID != "" {
		req.Version = &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{
				SliceId:   sliceID,
				SliceHash: query.Get("slice_version.slice_hash"),
			},
		}
	} else if commitHash := query.Get("commit_hash"); commitHash != "" {
		req.Version = &filev1.ListEntriesRequest_CommitHash{CommitHash: commitHash}
	}

	var md runtime.ServerMetadata
	resp, err := client.ListEntries(annotatedContext, req, grpc.Header(&md.HeaderMD), grpc.Trailer(&md.TrailerMD))
	annotatedContext = runtime.NewServerMetadataContext(annotatedContext, md)
	if err != nil {
		runtime.HTTPError(annotatedContext, mux, outboundMarshaler, w, r, err)
		return
	}
	runtime.ForwardResponseMessage(annotatedContext, mux, outboundMarshaler, w, r, resp, mux.GetForwardResponseOptions()...)
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
