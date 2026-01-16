package main

import (
	"log"
	"net"

	sliceservice "github.com/niczy/gitslice/internal/services/slice"
	"github.com/niczy/gitslice/internal/storage"
)

func main() {
	// Initialize storage
	st := storage.NewInMemoryStorage()

	// Initialize root slice
	if err := st.InitializeRootSlice(nil); err != nil {
		log.Printf("Warning: Failed to initialize root slice: %v", err)
	}

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := sliceservice.NewGRPCServer(st)

	log.Println("SliceService server listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
