package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	accountv1 "github.com/niczy/gitslice/proto/account"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// NewMux constructs a gRPC-Gateway mux backed by a single gRPC connection.
func NewMux(ctx context.Context, grpcAddr string) (*runtime.ServeMux, func(), error) {
	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	conn, err := grpc.DialContext(ctx, grpcAddr, opts...)
	if err != nil {
		return nil, func() {}, err
	}

	fileClient := filev1.NewFileServiceClient(conn)
	adminClient := adminv1.NewAdminServiceClient(conn)
	accountClient := accountv1.NewAccountServiceClient(conn)
	agentClient := agentv1.NewAgentServiceClient(conn)
	sliceClient := slicev1.NewSliceServiceClient(conn)

	if err := filev1.RegisterFileServiceHandlerClient(ctx, mux, fileClient); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}
	if err := adminv1.RegisterAdminServiceHandlerClient(ctx, mux, adminClient); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}
	if err := accountv1.RegisterAccountServiceHandlerClient(ctx, mux, accountClient); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}
	if err := agentv1.RegisterAgentServiceHandlerClient(ctx, mux, agentClient); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}
	if err := slicev1.RegisterSliceServiceHandlerClient(ctx, mux, sliceClient); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}

	if err := registerFileEntriesOverrides(mux, fileClient); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}

	closeConn := func() { _ = conn.Close() }
	return mux, closeConn, nil
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

// GRPCReady returns true if the gRPC endpoint is reachable within the timeout.
func GRPCReady(ctx context.Context, addr string) bool {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return false
	}
	return conn.Close() == nil
}

// WithCORS adds permissive CORS headers for local development.
func WithCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		handler.ServeHTTP(w, r)
	})
}
