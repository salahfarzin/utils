package db

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// realCert is a syntactically correct self-signed certificate
const realCert = `-----BEGIN CERTIFICATE-----
MIICpDCCAYwCCQCUHS1hxnWaMDANBgkqhkiG9w0BAQsFADAUMRIwEAYDVQQDDAls
b2NhbGhvc3QwHhcNMjYwMTI5MjE1NjEyWhcNMjcwMTI5MjE1NjEyWjAUMRIwEAYD
VQQDDAlsb2NhbGhvc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCo
DGlAuylF5JO5Oj3pIuii3fc3E8s3vZSj6qsdicqxspN7/atPo/nsDfAJQImAkm4+
Gmd6U6d6dDzvqsfNFdrDMfWmIyaOnRJZGcRphkhKgX8tbXOqGeAyeymdiqFU8TSe
j7mzDTauaieeG3MdJELbZzdKDU0lwUlKa4Qb3201aI111hMXSiqK8CJguV+msoFk
Ba3cG7R0vzl0hHKCo5DlTzU+LeulFDnXe12+x9erU5Zs64el5hZ/niLLEAxI5O/y
BpJcsFXotPvbIU4zrCa4QY31yXiE+3A5Wrlp01ucOgI60czFUlKx5ObtsAY86pgH
VascdicUYLjm7S0FbcndAgMBAAEwDQYJKoZIhvcNAQELBQADggEBAIOswOsZgAaB
B6d3mPcZdwPkWazvoOd4uxcJpWwpPseNOxH+f5DTxVJh8LMo2PsQ/zN1vDmumyTl
Qm0Qj5IkTz+Q5WdyyGQhGbt6BK0iakDYoo1xdBKuKb6tR/Mbi057DjKZH4+DwzPq
pn/dL/CqUNG4gVXAMhZLkqpJDEc64o2ujK30UjTsXsMYH1zDWQLUrY8jtMZ084ex
f08mhdGCHwDLBAHVnbfLP1BE+7yY23oNFknvdv4dABdLR4jlAcPyQgu/s8I6FCB9
XNrvNcLLOf8HFawE4hhgdMGv9BCGrUuw2qstbrKtUXOm8hnHSzrSZRZHW/fW+aHG
jf9/PoHdJbE=
-----END CERTIFICATE-----`

func TestNewMySQLStorage_InvalidSSLCA(t *testing.T) {
	cfg := MySQLConfig{
		User:     "user",
		Password: "password",
		Address:  "localhost:3306",
		Name:     "db",
		SSLCA:    "non-existent-file",
	}

	db, err := NewMySQLStorage(cfg)
	assert.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "failed to read CA cert")
}

func TestNewMySQLStorage_InvalidClientCerts(t *testing.T) {
	// Create a temporary file for SSL CA with a real cert
	caFile, err := os.CreateTemp("", "ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(caFile.Name())

	if _, err := caFile.WriteString(realCert); err != nil {
		t.Fatal(err)
	}
	caFile.Close()

	cfg := MySQLConfig{
		User:     "user",
		Password: "password",
		Address:  "localhost:3306",
		Name:     "db",
		SSLCA:    caFile.Name(),
		SSLCert:  "non-existent-cert",
		SSLKey:   "non-existent-key",
	}

	db, err := NewMySQLStorage(cfg)
	assert.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "failed to load client cert/key")
}

func TestNewMySQLStorage_PingFailure(t *testing.T) {
	// This test will attempt to connect to a non-existent MySQL server.
	// It should fail at the Ping stage.
	cfg := MySQLConfig{
		User:     "user",
		Password: "password",
		Address:  "127.0.0.1:1", // Invalid port/address
		Name:     "db",
	}

	// We expect a ping failure.

	db, err := NewMySQLStorage(cfg)
	assert.Error(t, err)
	assert.Nil(t, db)
}
