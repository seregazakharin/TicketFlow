package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Exp    int64  `json:"exp"`
}

func HashPassword(password, pepper string) (string, error) {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	sum := passwordDigest(password, pepper, hex.EncodeToString(salt[:]))
	return hex.EncodeToString(salt[:]) + ":" + sum, nil
}

func CheckPassword(encoded, password, pepper string) bool {
	parts := strings.Split(encoded, ":")
	if len(parts) != 2 {
		return false
	}
	expected := passwordDigest(password, pepper, parts[0])
	return hmac.Equal([]byte(expected), []byte(parts[1]))
}

func IssueToken(secret string, claims Claims, ttl time.Duration) (string, error) {
	claims.Exp = time.Now().Add(ttl).Unix()
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	return unsigned + "." + sign(secret, unsigned), nil
}

func VerifyToken(secret, token string) (Claims, error) {
	var claims Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, errors.New("invalid token format")
	}
	unsigned := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(sign(secret, unsigned)), []byte(parts[2])) {
		return claims, errors.New("invalid token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, err
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, err
	}
	if claims.Exp < time.Now().Unix() {
		return claims, errors.New("token expired at " + strconv.FormatInt(claims.Exp, 10))
	}
	return claims, nil
}

func passwordDigest(password, pepper, salt string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(salt))
	mac.Write([]byte(":"))
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))
}

func sign(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
