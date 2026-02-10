package workflow

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const testUsername = "testuser"

func withTestUser(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "User "+testUsername)
}
