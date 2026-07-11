package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMinBytes = 12
	passwordMaxBytes = 1024
	argonMemoryKiB   = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltBytes   = 16
	argonKeyBytes    = 32
)

func validatePassword(password string) error {
	if len(password) < passwordMinBytes {
		return fmt.Errorf("password must be at least %d bytes", passwordMinBytes)
	}
	if len(password) > passwordMaxBytes {
		return fmt.Errorf("password must be at most %d bytes", passwordMaxBytes)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemoryKiB, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, field := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return false
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return false
		}
		switch key {
		case "m":
			memory = n
		case "t":
			iterations = n
		case "p":
			parallelism = n
		default:
			return false
		}
	}
	// Treat database contents as untrusted input and cap Argon2 resource use.
	if memory < 8*1024 || memory > 64*1024 || iterations == 0 || iterations > 10 ||
		parallelism == 0 || parallelism > 16 || len(password) > passwordMaxBytes {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func bearerHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(bytes int) (string, error) {
	if bytes <= 0 || bytes > 128 {
		return "", errors.New("invalid random token size")
	}
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func secureEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
