package main

import (
	"log"
	"net"

	adminservice "github.com/niczy/gitslice/internal/services/admin"
	"github.com/niczy/gitslice/internal/storage"
)

func main() {
	// Initialize storage
	st := storage.NewInMemoryStorage()

	// Initialize root slice
	if err := st.InitializeRootSlice(nil); err != nil {
		log.Printf("Warning: Failed to initialize root slice: %v", err)
	}

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := adminservice.NewGRPCServer(st)

	log.Println("AdminService server listening on :50052")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
