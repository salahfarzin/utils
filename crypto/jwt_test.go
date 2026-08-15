package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return priv, &priv.PublicKey
}

func signTestToken(t *testing.T, priv *rsa.PrivateKey, subject string, expiresAt time.Time) string {
	t.Helper()
	claims := &Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject, ExpiresAt: jwt.NewNumericDate(expiresAt)}}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	require.NoError(t, err)
	return signed
}

func TestValidateJwtTokenWithKey_ValidToken(t *testing.T) {
	priv, pub := generateTestKeyPair(t)
	tokenStr := signTestToken(t, priv, "user-uuid-1", time.Now().Add(time.Hour))

	claims, err := ValidateJwtTokenWithKey(tokenStr, pub)

	require.NoError(t, err)
	assert.Equal(t, "user-uuid-1", claims.Subject)
}

func TestValidateJwtTokenWithKey_ExpiredToken(t *testing.T) {
	priv, pub := generateTestKeyPair(t)
	tokenStr := signTestToken(t, priv, "user-uuid-1", time.Now().Add(-time.Hour))

	_, err := ValidateJwtTokenWithKey(tokenStr, pub)

	assert.Error(t, err)
}

func TestValidateJwtTokenWithKey_WrongKeyRejected(t *testing.T) {
	priv, _ := generateTestKeyPair(t)
	_, otherPub := generateTestKeyPair(t)
	tokenStr := signTestToken(t, priv, "user-uuid-1", time.Now().Add(time.Hour))

	_, err := ValidateJwtTokenWithKey(tokenStr, otherPub)

	assert.Error(t, err)
}

func TestGenerateJwtToken(t *testing.T) {
	// Generate a test private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tests := []struct {
		name        string
		params      TokenParams
		expectError bool
	}{
		{
			name: "valid token generation",
			params: TokenParams{
				Subject:           "test-user",
				PrivateKey:        privateKey,
				ExpirationSeconds: 3600,
				JTI:               "test-jti",
			},
			expectError: false,
		},
		{
			name: "token without JTI",
			params: TokenParams{
				Subject:           "test-user",
				PrivateKey:        privateKey,
				ExpirationSeconds: 3600,
			},
			expectError: false,
		},
		{
			name: "zero expiration",
			params: TokenParams{
				Subject:           "test-user",
				PrivateKey:        privateKey,
				ExpirationSeconds: 0,
			},
			expectError: true, // Zero expiration should fail
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := GenerateJwtToken(tt.params)

			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, token)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, token)

				// Verify token can be parsed
				parsedToken, err := jwt.ParseWithClaims(token, &Claims{}, func(token *jwt.Token) (interface{}, error) {
					return &privateKey.PublicKey, nil
				})
				assert.NoError(t, err)
				assert.True(t, parsedToken.Valid)

				claims, ok := parsedToken.Claims.(*Claims)
				assert.True(t, ok)
				assert.Equal(t, tt.params.Subject, claims.Subject)
				if tt.params.JTI != "" {
					assert.Equal(t, tt.params.JTI, claims.ID)
				} else {
					assert.NotEmpty(t, claims.ID)
				}
			}
		})
	}
}

func TestValidateJwtToken(t *testing.T) {
	// Create temporary key files for testing
	tempDir := t.TempDir()
	privateKeyPath := filepath.Join(tempDir, "private.pem")
	publicKeyPath := filepath.Join(tempDir, "public.pem")

	// Generate test keys
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Save private key
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	privateKeyFile, err := os.Create(privateKeyPath)
	require.NoError(t, err)
	err = pem.Encode(privateKeyFile, privateKeyPEM)
	require.NoError(t, err)
	privateKeyFile.Close()

	// Save public key
	publicKeyPEM := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey),
	}
	publicKeyFile, err := os.Create(publicKeyPath)
	require.NoError(t, err)
	err = pem.Encode(publicKeyFile, publicKeyPEM)
	require.NoError(t, err)
	publicKeyFile.Close()

	// Generate a valid token
	tokenParams := TokenParams{
		Subject:           "test-user",
		PrivateKey:        privateKey,
		ExpirationSeconds: 3600,
		JTI:               "test-jti",
	}
	validToken, err := GenerateJwtToken(tokenParams)
	require.NoError(t, err)

	// Generate an expired token
	expiredTokenParams := TokenParams{
		Subject:           "test-user",
		PrivateKey:        privateKey,
		ExpirationSeconds: -1, // Already expired
	}
	expiredToken, err := GenerateJwtToken(expiredTokenParams)
	require.NoError(t, err)

	tests := []struct {
		name          string
		token         string
		publicKeyPath string
		expectError   bool
	}{
		{
			name:          "valid token",
			token:         validToken,
			publicKeyPath: publicKeyPath,
			expectError:   false,
		},
		{
			name:          "expired token",
			token:         expiredToken,
			publicKeyPath: publicKeyPath,
			expectError:   true,
		},
		{
			name:          "invalid token",
			token:         "invalid.jwt.token",
			publicKeyPath: publicKeyPath,
			expectError:   true,
		},
		{
			name:          "non-existent public key",
			token:         validToken,
			publicKeyPath: "/non/existent/key.pem",
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := ValidateJwtToken(tt.token, tt.publicKeyPath)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, claims)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, claims)
				assert.Equal(t, "test-user", claims.Subject)
				assert.Equal(t, "test-jti", claims.ID)
			}
		})
	}
}

func TestLoadJwtPrivateKey(t *testing.T) {
	tempDir := t.TempDir()
	validKeyPath := filepath.Join(tempDir, "valid_private.pem")
	invalidKeyPath := filepath.Join(tempDir, "invalid_private.pem")

	// Generate and save a valid private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	validPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	validFile, err := os.Create(validKeyPath)
	require.NoError(t, err)
	err = pem.Encode(validFile, validPEM)
	require.NoError(t, err)
	validFile.Close()

	// Create an invalid key file
	invalidFile, err := os.Create(invalidKeyPath)
	require.NoError(t, err)
	_, err = invalidFile.WriteString("invalid key data")
	require.NoError(t, err)
	invalidFile.Close()

	tests := []struct {
		name        string
		keyPath     string
		expectError bool
	}{
		{
			name:        "valid private key",
			keyPath:     validKeyPath,
			expectError: false,
		},
		{
			name:        "non-existent file",
			keyPath:     "/non/existent/key.pem",
			expectError: true,
		},
		{
			name:        "invalid key format",
			keyPath:     invalidKeyPath,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := LoadJwtPrivateKey(tt.keyPath)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, key)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, key)
				assert.Equal(t, privateKey.N, key.N)
			}
		})
	}
}

func TestLoadJwtPublicKey(t *testing.T) {
	tempDir := t.TempDir()
	validKeyPath := filepath.Join(tempDir, "valid_public.pem")
	invalidKeyPath := filepath.Join(tempDir, "invalid_public.pem")

	// Generate and save a valid public key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	validPEM := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey),
	}
	validFile, err := os.Create(validKeyPath)
	require.NoError(t, err)
	err = pem.Encode(validFile, validPEM)
	require.NoError(t, err)
	validFile.Close()

	// Create an invalid key file
	invalidFile, err := os.Create(invalidKeyPath)
	require.NoError(t, err)
	_, err = invalidFile.WriteString("invalid key data")
	require.NoError(t, err)
	invalidFile.Close()

	tests := []struct {
		name        string
		keyPath     string
		expectError bool
	}{
		{
			name:        "valid public key",
			keyPath:     validKeyPath,
			expectError: false,
		},
		{
			name:        "non-existent file",
			keyPath:     "/non/existent/key.pem",
			expectError: true,
		},
		{
			name:        "invalid key format",
			keyPath:     invalidKeyPath,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := LoadJwtPublicKey(tt.keyPath)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, key)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, key)
				assert.Equal(t, &privateKey.PublicKey, key)
			}
		})
	}
}

func TestGenerateAccessToken(t *testing.T) {
	tempDir := t.TempDir()
	privateKeyPath := filepath.Join(tempDir, "private.pem")

	// Generate and save a private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	keyFile, err := os.Create(privateKeyPath)
	require.NoError(t, err)
	err = pem.Encode(keyFile, privateKeyPEM)
	require.NoError(t, err)
	keyFile.Close()

	tests := []struct {
		name        string
		params      AccessTokenParams
		expectError bool
	}{
		{
			name: "valid access token",
			params: AccessTokenParams{
				Subject:           "test-user",
				PrivateKeyPath:    privateKeyPath,
				ExpirationSeconds: 3600,
				JTI:               "test-jti",
			},
			expectError: false,
		},
		{
			name: "access token without JTI",
			params: AccessTokenParams{
				Subject:           "test-user",
				PrivateKeyPath:    privateKeyPath,
				ExpirationSeconds: 1800,
			},
			expectError: false,
		},
		{
			name: "invalid private key path",
			params: AccessTokenParams{
				Subject:           "test-user",
				PrivateKeyPath:    "/non/existent/key.pem",
				ExpirationSeconds: 3600,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := GenerateAccessToken(tt.params)

			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, token)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, token)

				// Verify token can be parsed
				parsedToken, err := jwt.ParseWithClaims(token, &Claims{}, func(token *jwt.Token) (interface{}, error) {
					return &privateKey.PublicKey, nil
				})
				assert.NoError(t, err)
				assert.True(t, parsedToken.Valid)

				claims, ok := parsedToken.Claims.(*Claims)
				assert.True(t, ok)
				assert.Equal(t, tt.params.Subject, claims.Subject)
				if tt.params.JTI != "" {
					assert.Equal(t, tt.params.JTI, claims.ID)
				} else {
					assert.NotEmpty(t, claims.ID)
				}
			}
		})
	}
}
