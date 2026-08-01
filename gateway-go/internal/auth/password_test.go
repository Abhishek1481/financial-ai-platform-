package auth

import "testing"

func TestHashAndVerifyPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if hash == "correct-horse-battery" {
		t.Fatal("HashPassword() returned the plaintext unchanged")
	}
	if err := VerifyPassword(hash, "correct-horse-battery"); err != nil {
		t.Errorf("VerifyPassword() with correct password: %v", err)
	}
}

func TestVerifyPassword_WrongPasswordFails(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if err := VerifyPassword(hash, "wrong-password"); err == nil {
		t.Error("VerifyPassword() with wrong password: expected error, got nil")
	}
}
