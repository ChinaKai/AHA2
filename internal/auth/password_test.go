package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	t.Parallel()
	params := PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	hash, err := HashPassword("correct-horse-battery", params)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-battery") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(hash, "incorrect-password") {
		t.Fatal("invalid password accepted")
	}
}
