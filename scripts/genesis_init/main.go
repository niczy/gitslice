package main

import (
	"context"
	"log"

	"github.com/niczy/gitslice/internal/bootstrap"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	sliceservice "github.com/niczy/gitslice/services/slice"
)

func main() {
	cfg := config.LoadConfig()
	ctx := context.Background()

	st, closeStorage, err := bootstrap.InitStorage(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to initialize storage backend: %v", err)
	}
	defer closeStorage()

	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		log.Fatalf("Failed to initialize root slice: %v", err)
	}

	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		log.Fatalf("Genesis initialization failed: %v", err)
	}

	log.Println("Genesis initialization completed successfully")
}
