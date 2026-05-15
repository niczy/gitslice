package authz

import (
	"context"
	"strings"

	"github.com/niczy/gitslice/internal/adminauth"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type UserGetter interface {
	GetUser(ctx context.Context, username string) (*models.User, error)
}

func HasSliceViewAccess(slice *models.Slice, username string) bool {
	if slice == nil {
		return false
	}
	if slice.IsRoot {
		return false
	}
	return HasSliceOwnerAccess(slice, username)
}

func HasSliceOwnerAccess(slice *models.Slice, username string) bool {
	if slice == nil {
		return false
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	if slice.CreatedBy == username {
		return true
	}
	for _, owner := range slice.Owners {
		if owner == username {
			return true
		}
	}
	return false
}

func HasSliceViewAccessForUser(slice *models.Slice, user *models.User) bool {
	if slice == nil || user == nil {
		return false
	}
	if slice.IsRoot {
		return adminauth.IsAdminUser(user)
	}
	return HasSliceOwnerAccess(slice, user.Username)
}

func CanViewSlice(ctx context.Context, users UserGetter, slice *models.Slice, username string) (bool, error) {
	if slice == nil {
		return false, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return false, nil
	}
	if !slice.IsRoot {
		return HasSliceOwnerAccess(slice, username), nil
	}
	if users == nil {
		return false, nil
	}
	user, err := users.GetUser(ctx, username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return false, nil
		}
		return false, err
	}
	return adminauth.IsAdminUser(user), nil
}
