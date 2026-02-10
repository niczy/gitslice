package authz

import "github.com/niczy/gitslice/internal/models"

func HasSliceViewAccess(slice *models.Slice, username string) bool {
	if slice == nil {
		return false
	}
	if slice.IsRoot {
		return true
	}
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
