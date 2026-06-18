package auth

import (
	"testing"
	"time"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("secret123", "pepper")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !CheckPassword(hash, "secret123", "pepper") {
		t.Fatal("expected password to match")
	}
	if CheckPassword(hash, "wrong", "pepper") {
		t.Fatal("expected wrong password to fail")
	}
}

func TestTokenIssueAndVerify(t *testing.T) {
	token, err := IssueToken("secret", Claims{UserID: "usr_1", Email: "dev@example.com"}, time.Minute)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	claims, err := VerifyToken("secret", token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims.UserID != "usr_1" || claims.Email != "dev@example.com" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestTokenRejectsWrongSecret(t *testing.T) {
	token, err := IssueToken("secret", Claims{UserID: "usr_1"}, time.Minute)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if _, err := VerifyToken("other-secret", token); err == nil {
		t.Fatal("expected invalid signature")
	}
}
