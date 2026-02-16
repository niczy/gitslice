module github.com/niczy/gitslice

go 1.24

require (
	cloud.google.com/go/storage v1.43.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.20.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/niczy/gitslice/services/admin v0.0.0
	github.com/niczy/gitslice/services/file v0.0.0
	github.com/niczy/gitslice/services/slice v0.0.0
	google.golang.org/api v0.224.0
	google.golang.org/genproto/googleapis/api v0.0.0-20240513163218-0867130af1f8
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.34.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/net v0.23.0 // indirect
	golang.org/x/sys v0.18.0 // indirect
	golang.org/x/text v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240513163218-0867130af1f8 // indirect
)

replace github.com/niczy/gitslice/services/admin => ./services/admin

replace github.com/niczy/gitslice/services/file => ./services/file

replace github.com/niczy/gitslice/services/slice => ./services/slice
