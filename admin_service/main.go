package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

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

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %v, shutting down AdminService gracefully...", sig)
		s.GracefulStop()
	}()

	log.Printf("AdminService server listening on %s", addr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
	log.Println("AdminService server stopped")
}
