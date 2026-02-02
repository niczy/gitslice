module github.com/niczy/gitslice/servers/core

go 1.24

require (
	github.com/niczy/gitslice v0.0.0
	github.com/niczy/gitslice/services/admin v0.0.0
	github.com/niczy/gitslice/services/file v0.0.0
	github.com/niczy/gitslice/services/slice v0.0.0
	google.golang.org/grpc v1.64.0
)

replace github.com/niczy/gitslice => ../..

replace github.com/niczy/gitslice/services/admin => ../../services/admin

replace github.com/niczy/gitslice/services/file => ../../services/file

replace github.com/niczy/gitslice/services/slice => ../../services/slice
