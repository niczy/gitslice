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

	// Initialize root slice and populate from git via changeset API
	ctx := context.Background()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		log.Fatalf("Failed to initialize root slice: %v", err)
	}
	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		log.Printf("Warning: genesis population failed: %v", err)
	}

	grpcAddr := cfg.GetSliceServiceAddr()

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := sliceservice.NewGRPCServer(st)
	filev1.RegisterFileServiceServer(s, fileservice.NewService(st))

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %v, shutting down SliceService gracefully...", sig)
		s.GracefulStop()
	}()

	log.Printf("SliceService server listening on %s", grpcAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
	log.Println("SliceService server stopped")
}
