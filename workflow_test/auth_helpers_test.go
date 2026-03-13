package workflow

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const testUsername = "testuser"

func withTestUser(ctx context.Context) context.Context {
	return withUsername(ctx, testUsername)
}

func withUsername(ctx context.Context, username string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "User "+username)
}

func withBearerToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}
