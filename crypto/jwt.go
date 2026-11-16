package crypto

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AccessTokenParams struct {
	Subject           string
	PrivateKeyPath    string
	ExpirationSeconds int64
	JTI               string
}

// TokenParams holds parameters for generating a JWT
type TokenParams struct {
	Subject           string
	PrivateKey        *rsa.PrivateKey
	ExpirationSeconds int64
	JTI               string // JWT ID
	// Add more fields for custom claims as needed
}

// Claims wraps jwt.RegisteredClaims for extensibility
type Claims struct {
	jwt.RegisteredClaims
}

// GenerateJwtToken creates a signed JWT string for the given params
func GenerateJwtToken(params TokenParams) (string, error) {
	// Validate expiration seconds - zero expiration is not allowed
	if params.ExpirationSeconds == 0 {
		return "", errors.New("expiration seconds must not be zero")
	}

	// Use JTI from params if provided, otherwise generate a unique one
	jti := params.JTI
	if jti == "" {
		jti = uuid.New().String()
	}

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Duration(params.ExpirationSeconds) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			Subject:   params.Subject,
			ID:        jti,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(params.PrivateKey)
}

// ValidateJwtToken parses and validates a JWT string, returning the claims if valid
func ValidateJwtToken(tokenStr string, publicKeyPath string) (*Claims, error) {
	publicKey, err := LoadJwtPublicKey(publicKeyPath)
	if err != nil {
		return nil, err
	}
	return ValidateJwtTokenWithKey(tokenStr, publicKey)
}

// ValidateJwtTokenWithKey parses and validates a JWT string against an
// already-loaded public key, avoiding a file read/parse per call.
func ValidateJwtTokenWithKey(tokenStr string, publicKey *rsa.PublicKey) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return publicKey, nil
	})

	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// Load RSA private key
func LoadJwtPrivateKey(path string) (*rsa.PrivateKey, error) {
	if err := validateFilePath(path); err != nil {
		return nil, err
	}

	keyData, err := os.ReadFile(path) // #nosec G304 - Path is validated by validateFilePath
	if err != nil {
		return nil, err
	}
	return jwt.ParseRSAPrivateKeyFromPEM(keyData)
}

// validateFilePath checks if the file path is safe and within allowed directories
func validateFilePath(path string) error {
	// Get the absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Clean the path to resolve any .. or . components
	cleanPath := filepath.Clean(absPath)

	// Get the project root directory (the calling service's cwd, walking up to its go.mod)
	projectRoot := getProjectRoot()

	// Define allowed directories relative to project root
	allowedDirs := []string{
		filepath.Join(projectRoot, "storage", "keys"),
		filepath.Join(projectRoot, "keys"), // Alternative location
		// Allow temp directories for testing
		os.TempDir(),
	}

	// Check if the path is within an allowed directory
	isAllowed := false
	for _, allowedDir := range allowedDirs {
		allowedAbs, err := filepath.Abs(allowedDir)
		if err != nil {
			continue
		}

		relPath, err := filepath.Rel(allowedAbs, cleanPath)
		if err != nil {
			continue
		}

		// If the relative path doesn't start with .., it's within the allowed directory
		if !strings.HasPrefix(relPath, "..") {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		return fmt.Errorf("file path is not within allowed directories: %s", cleanPath)
	}

	// Additional security checks
	if strings.Contains(cleanPath, "../../../") || strings.Contains(cleanPath, "..\\..\\..\\") {
		return errors.New("invalid file path: excessive directory traversal not allowed")
	}

	return nil
}

// getProjectRoot returns the calling service's project root directory
func getProjectRoot() string {
	// Try to find the project root by looking for go.mod
	dir, err := os.Getwd()
	if err != nil {
		// Fallback
		return "."
	}

	// Walk up the directory tree looking for go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}

	// Fallback to current working directory
	return "."
}

// Load RSA public key
func LoadJwtPublicKey(path string) (*rsa.PublicKey, error) {
	if err := validateFilePath(path); err != nil {
		return nil, err
	}

	keyData, err := os.ReadFile(path) // #nosec G304 - Path is validated by validateFilePath
	if err != nil {
		return nil, err
	}
	return jwt.ParseRSAPublicKeyFromPEM(keyData)
}

func GenerateAccessToken(params AccessTokenParams) (string, error) {
	privateKey, err := LoadJwtPrivateKey(params.PrivateKeyPath)
	if err != nil {
		return "", err
	}
	return GenerateJwtToken(TokenParams{
		Subject:           params.Subject,
		PrivateKey:        privateKey,
		ExpirationSeconds: params.ExpirationSeconds,
		JTI:               params.JTI,
	})
}
