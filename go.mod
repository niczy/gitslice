module github.com/niczy/gitslice

go 1.24

require (
	cloud.google.com/go/storage v1.43.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.1
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.20.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/niczy/gitslice/services/admin v0.0.0
	github.com/niczy/gitslice/services/file v0.0.0
	github.com/niczy/gitslice/services/slice v0.0.0
	github.com/pmezard/go-difflib v1.0.0
	golang.org/x/crypto v0.37.0
	golang.org/x/sys v0.32.0
	google.golang.org/api v0.224.0
	google.golang.org/genproto/googleapis/api v0.0.0-20240513163218-0867130af1f8
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.34.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.23.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240513163218-0867130af1f8 // indirect
)

replace github.com/niczy/gitslice/services/admin => ./services/admin

replace github.com/niczy/gitslice/services/file => ./services/file

replace github.com/niczy/gitslice/services/slice => ./services/slice

// Offline build stubs: provide minimal implementations so the codebase can be
// compiled without network access. These stubs expose only the surface area
// actually used by internal/storage and servers/core; they are NOT meant for
// production use.
replace cloud.google.com/go/storage => ./stubs/cloudstore

replace google.golang.org/api => ./stubs/googleapi
