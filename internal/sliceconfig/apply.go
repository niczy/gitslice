package sliceconfig

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/niczy/gitslice/internal/storage"
)

func ApplyConfig(ctx context.Context, st storage.Storage, cfg *SliceConfigFile) error {
	if cfg == nil {
		return nil
	}

	for name, entry := range cfg.Environments {
		env, err := st.GetEnvironment(ctx, name)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) || errors.Is(err, storage.ErrInvalidInput) {
				log.Printf("sliceconfig: environment %q declared in config but not registered", name)
				continue
			}
			return err
		}
		if env.DisplayName != entry.DisplayName {
			env.DisplayName = entry.DisplayName
			if err := st.UpdateEnvironment(ctx, env); err != nil {
				return err
			}
		}
	}

	for sliceRef, entry := range cfg.Slices {
		envName := strings.TrimSpace(entry.Environment)
		if envName == "" {
			continue
		}
		slice, err := st.GetSlice(ctx, sliceRef)
		if err != nil {
			if !errors.Is(err, storage.ErrSliceNotFound) {
				return err
			}
			slice, err = st.GetSliceBySlug(ctx, sliceRef)
			if err != nil {
				if errors.Is(err, storage.ErrSliceNotFound) {
					continue
				}
				return err
			}
		}
		if slice.Environment != envName {
			if err := st.UpdateSliceEnvironment(ctx, slice.ID, envName); err != nil {
				return err
			}
		}
	}

	return nil
}

func ApplyFromFileTree(ctx context.Context, st storage.Storage) error {
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		return err
	}
	file, err := storage.ReadSliceFileContent(ctx, st, rootSlice.ID, ConfigFilePath)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil
		}
		return err
	}
	cfg, err := ParseConfig(file.Content)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", ConfigFilePath, err)
	}
	return ApplyConfig(ctx, st, cfg)
}
