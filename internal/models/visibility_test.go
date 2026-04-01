package models

import "testing"

func TestNormalizeVisibilityDefaultsToPrivate(t *testing.T) {
	if got := NormalizeVisibility(""); got != VisibilityPrivate {
		t.Fatalf("NormalizeVisibility(\"\") = %q, want %q", got, VisibilityPrivate)
	}
	if got := NormalizeVisibility("unexpected"); got != VisibilityPrivate {
		t.Fatalf("NormalizeVisibility(unexpected) = %q, want %q", got, VisibilityPrivate)
	}
}

func TestNormalizeVisibilityPublic(t *testing.T) {
	if got := NormalizeVisibility("PUBLIC"); got != VisibilityPublic {
		t.Fatalf("NormalizeVisibility(PUBLIC) = %q, want %q", got, VisibilityPublic)
	}
	if !VisibilityPublic.IsPublic() {
		t.Fatal("VisibilityPublic.IsPublic() = false, want true")
	}
	if VisibilityPrivate.IsPublic() {
		t.Fatal("VisibilityPrivate.IsPublic() = true, want false")
	}
}
