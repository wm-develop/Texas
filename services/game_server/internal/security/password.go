package security

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	DefaultPasswordIterations = 210_000
	passwordSaltBytes         = 16
	passwordKeyBytes          = 32
)

type PasswordHasher struct {
	iterations int
	random     io.Reader
}

func NewPasswordHasher(iterations int, random io.Reader) (*PasswordHasher, error) {
	if iterations < 1_000 {
		return nil, errors.New("password hash iterations are too low")
	}
	if random == nil {
		random = rand.Reader
	}
	return &PasswordHasher{iterations: iterations, random: random}, nil
}

func (hasher *PasswordHasher) Hash(password string) (string, error) {
	if len(password) < 8 || len(password) > 128 {
		return "", errors.New("password length must be between 8 and 128 bytes")
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := io.ReadFull(hasher.random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hasher.iterations, passwordKeyBytes)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"pbkdf2_sha256$%d$%s$%s",
		hasher.iterations,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key),
	), nil
}

func (hasher *PasswordHasher) Verify(password string, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1_000 || iterations > 2_000_000 {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(salt) != passwordSaltBytes {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(want) != passwordKeyBytes {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
