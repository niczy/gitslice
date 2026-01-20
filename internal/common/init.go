package common

import (
	"context"
	"fmt"
	"log"

	"github.com/niczy/gitslice/internal/storage"
)

// EnsureRootSliceInitialized initializes the root slice if it doesn't exist.
// It returns an error only if initialization fails critically.
// This function is idempotent and safe to call multiple times.
func EnsureRootSliceInitialized(ctx context.Context, st storage.Storage) error {
	// Check if root slice already exists
	_, err := st.GetRootSlice(ctx)
	if err == nil {
		// Root slice already exists, nothing to do
		return nil
	}

	// Root slice doesn't exist, initialize it
	if err := st.InitializeRootSlice(ctx); err != nil {
		return fmt.Errorf("failed to initialize root slice: %w", err)
	}

	log.Println("Root slice initialized successfully")
	return nil
}
