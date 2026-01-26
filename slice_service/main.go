package main

import (
	"context"
	"log"
	"net"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	fileservice "github.com/niczy/gitslice/internal/services/file"
	sliceservice "github.com/niczy/gitslice/internal/services/slice"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
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

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := sliceservice.NewGRPCServer(st)
	filev1.RegisterFileServiceServer(s, fileservice.NewService(st))

	log.Printf("SliceService server listening on %s", grpcAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
