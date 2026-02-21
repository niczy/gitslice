package accountservice

import (
	"github.com/niczy/gitslice/internal/storage"
	accountv1 "github.com/niczy/gitslice/proto/account"
	"google.golang.org/grpc"
)

type accountServiceServer struct {
	accountv1.UnimplementedAccountServiceServer
	st storage.Storage
}

// RegisterGRPCServer registers the account service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	accountv1.RegisterAccountServiceServer(srv, &accountServiceServer{st: st})
}
