package main

import (
	"log"
	"net"

	"github.com/niczy/gitslice/internal/server"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	"google.golang.org/grpc"
)

func main() {
	st := storage.NewInMemoryStorage()

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	adminv1.RegisterAdminServiceServer(s, server.NewAdminServiceServer(st))

	log.Println("AdminService server listening on :50052")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
