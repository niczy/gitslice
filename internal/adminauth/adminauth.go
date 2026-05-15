package adminauth

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

func ConfiguredAdminEmails() map[string]struct{} {
	out := make(map[string]struct{})
	for _, email := range ParseAdminEmailValue(os.Getenv("ADMIN_USER_EMAILS")) {
		out[email] = struct{}{}
	}
	return out
}

func ParseAdminEmailValue(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var parsed []string
	if strings.HasPrefix(raw, "[") && json.Unmarshal([]byte(raw), &parsed) == nil {
		return normalizeAdminEmails(parsed)
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	return normalizeAdminEmails(parts)
}

func AdminStatusForEmail(email string) (configured bool, isAdmin bool, primaryEmail string) {
	admins := ConfiguredAdminEmails()
	primaryEmail = strings.ToLower(strings.TrimSpace(email))
	if primaryEmail == "" {
		return len(admins) > 0, false, ""
	}
	_, isAdmin = admins[primaryEmail]
	return len(admins) > 0, isAdmin, primaryEmail
}

func IsAdminUser(user *models.User) bool {
	if user == nil {
		return false
	}
	_, isAdmin, _ := AdminStatusForEmail(user.PrimaryEmail)
	return isAdmin
}

func normalizeAdminEmails(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		email := strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`))
		if email == "" || !strings.Contains(email, "@") {
			continue
		}
		out = append(out, email)
	}
	return out
}
