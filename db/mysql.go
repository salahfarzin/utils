package db

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQLConfig holds the configuration for the MySQL connection.
type MySQLConfig struct {
	User            string
	Password        string
	Address         string
	Name            string
	SSLCA           string
	SSLCert         string
	SSLKey          string
	SSLVerify       bool
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime int
}

func NewMySQLStorage(cfg MySQLConfig) (*sql.DB, error) {
	mysqlCfg := mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Addr:                 cfg.Address,
		DBName:               cfg.Name,
		Net:                  "tcp",
		AllowNativePasswords: true,
		ParseTime:            true,
	}

	if cfg.SSLCA != "" {
		rootCertPool := x509.NewCertPool()
		pem, err := os.ReadFile(cfg.SSLCA)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert from %s: %w", cfg.SSLCA, err)
		}
		if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
			return nil, fmt.Errorf("failed to append CA cert from %s", cfg.SSLCA)
		}

		clientCert := make([]tls.Certificate, 0, 1)
		if cfg.SSLCert != "" && cfg.SSLKey != "" {
			certs, err := tls.LoadX509KeyPair(cfg.SSLCert, cfg.SSLKey)
			if err != nil {
				return nil, fmt.Errorf("failed to load client cert/key (%s/%s): %w", cfg.SSLCert, cfg.SSLKey, err)
			}
			clientCert = append(clientCert, certs)
		}

		tlsConfigName := "custom"
		_ = mysql.RegisterTLSConfig(tlsConfigName, &tls.Config{
			RootCAs:            rootCertPool,
			Certificates:       clientCert,
			InsecureSkipVerify: !cfg.SSLVerify,
		})
		mysqlCfg.TLSConfig = tlsConfigName
	}

	dsn := mysqlCfg.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
