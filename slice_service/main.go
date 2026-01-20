package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/niczy/gitslice/internal/models"
	fileservice "github.com/niczy/gitslice/internal/services/file"
	sliceservice "github.com/niczy/gitslice/internal/services/slice"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Initialize storage
	st := storage.NewInMemoryStorage()

	// Initialize root slice
	if err := st.InitializeRootSlice(nil); err != nil {
		log.Printf("Warning: Failed to initialize root slice: %v", err)
	}

	grpcAddr := ":50051"
	gatewayAddr := ":8080"

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
	gatewayMux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := filev1.RegisterFileServiceHandlerFromEndpoint(ctx, gatewayMux, "localhost"+grpcAddr, opts); err != nil {
		log.Fatalf("Failed to register file service gateway: %v", err)
	}

	rootMux := http.NewServeMux()
	rootMux.Handle("/v1/slices", sliceHandler(st))
	rootMux.Handle("/", gatewayMux)

	handler := withCORS(rootMux)
	server := &http.Server{
		Addr:    gatewayAddr,
		Handler: handler,
	}

	log.Printf("FileService gateway listening on %s", gatewayAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve gateway: %v", err)
	}
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

type sliceCreateRequest struct {
	SliceID     string   `json:"slice_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
	Owners      []string `json:"owners"`
	CreatedBy   string   `json:"created_by"`
}

type sliceSummary struct {
	SliceID     string   `json:"slice_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Owners      []string `json:"owners"`
	CreatedBy   string   `json:"created_by"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	FileCount   int      `json:"file_count"`
	IsRoot      bool     `json:"is_root"`
}

type sliceListResponse struct {
	Slices []sliceSummary `json:"slices"`
}

type sliceCreateResponse struct {
	SliceID string `json:"slice_id"`
	Status  string `json:"status"`
}

func sliceHandler(st storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListSlices(w, r, st)
		case http.MethodPost:
			handleCreateSlice(w, r, st)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleListSlices(w http.ResponseWriter, r *http.Request, st storage.Storage) {
	limit := 100
	offset := 0
	if value := r.URL.Query().Get("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	slices, err := st.ListSlices(r.Context(), limit, offset)
	if err != nil {
		http.Error(w, "failed to list slices", http.StatusInternalServerError)
		return
	}

	response := sliceListResponse{
		Slices: make([]sliceSummary, 0, len(slices)),
	}
	for _, slice := range slices {
		response.Slices = append(response.Slices, sliceSummary{
			SliceID:     slice.ID,
			Name:        slice.Name,
			Description: slice.Description,
			Owners:      append([]string{}, slice.Owners...),
			CreatedBy:   slice.CreatedBy,
			CreatedAt:   slice.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   slice.UpdatedAt.Format(time.RFC3339),
			FileCount:   len(slice.Files),
			IsRoot:      slice.IsRoot,
		})
	}

	writeJSON(w, response, http.StatusOK)
}

func handleCreateSlice(w http.ResponseWriter, r *http.Request, st storage.Storage) {
	var req sliceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	if req.SliceID == "" {
		http.Error(w, "slice_id is required", http.StatusBadRequest)
		return
	}

	slice := &models.Slice{
		ID:          req.SliceID,
		Name:        req.Name,
		Description: req.Description,
		Files:       req.Files,
		Owners:      req.Owners,
		CreatedBy:   req.CreatedBy,
	}

	if err := st.CreateSlice(r.Context(), slice); err != nil {
		if errors.Is(err, storage.ErrSliceAlreadyExists) {
			http.Error(w, "slice already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create slice", http.StatusInternalServerError)
		return
	}

	writeJSON(w, sliceCreateResponse{SliceID: slice.ID, Status: "created"}, http.StatusCreated)
}

func writeJSON(w http.ResponseWriter, payload any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
