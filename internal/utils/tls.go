package utils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// NewTLSConfig creates a tls.Config based on the provided settings.
func NewTLSConfig(certPath, keyPath, caPath, serverName string, insecureSkipVerify bool) (*tls.Config, error) {
	var cert tls.Certificate
	var err error

	// Load client/server certificate and key if provided
	if certPath != "" && keyPath != "" {
		cert, err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load certificate/key: %w", err)
		}
	}

	// Load CA certificate if provided
	var caPool *x509.CertPool
	if caPath != "" {
		caCert, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caPool = x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA certificate to pool")
		}
	}

	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool, // For Client verifying Server
		ClientCAs:          caPool, // For Server verifying Client (mTLS)
		ServerName:         serverName,
		InsecureSkipVerify: insecureSkipVerify,
	}, nil
}
