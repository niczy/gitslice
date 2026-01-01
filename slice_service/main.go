package main

import (
	"log"
	"net"

	"github.com/niczy/gitslice/internal/server"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
)

func main() {
	st := storage.NewInMemoryStorage()

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	slicev1.RegisterSliceServiceServer(s, server.NewSliceServiceServer(st))

	log.Println("SliceService server listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
