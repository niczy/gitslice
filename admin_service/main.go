package main

import (
	"context"
	"log"
	"net"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	adminservice "github.com/niczy/gitslice/internal/services/admin"
	"github.com/niczy/gitslice/internal/storage"
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

	addr := cfg.GetAdminServiceAddr()
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := adminservice.NewGRPCServer(st)

	log.Printf("AdminService server listening on %s", addr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
