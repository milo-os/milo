package cockroach

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestOpenRejectsNegativeLimits(t *testing.T) {
	for name, opt := range map[string]Option{
		"connections": MaxConns(-1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Open(t.Context(), "postgresql://localhost/milo", opt)
			require.ErrorContains(t, err, "cannot be negative")
		})
	}
}

// writeClientCert writes a fresh self-signed certificate with serial number
// serial, and its key, to dir.
func writeClientCert(t *testing.T, dir string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "root"}, NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
}

// A rotated client certificate is what the next connection presents.
func TestClientCertReloadsAtHandshake(t *testing.T) {
	dir := t.TempDir()
	writeClientCert(t, dir, 1)
	dsn := "postgresql://root@localhost/milo?sslmode=require&sslcert=" + filepath.Join(dir, "tls.crt") + "&sslkey=" + filepath.Join(dir, "tls.key")
	cfg, err := pgconn.ParseConfig(dsn)
	require.NoError(t, err)
	reloadClientCert(cfg, dsn)
	serial := func() int64 {
		cert, err := cfg.TLSConfig.GetClientCertificate(nil)
		require.NoError(t, err)
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		require.NoError(t, err)
		return leaf.SerialNumber.Int64()
	}
	require.Equal(t, int64(1), serial())
	writeClientCert(t, dir, 2)
	require.Equal(t, int64(2), serial())
}

// Open refuses a schema behind the binary, and accepts one ahead of it, which
// a rollout or a rollback meets while older and newer binaries both run.
func TestOpenChecksTheSchemaVersion(t *testing.T) {
	testBackend(t)
	ctx := t.Context()
	dsn, drop, err := createDatabase(ctx, baseDSN, fmt.Sprintf("milo_storage_unmigrated_%d", time.Now().UnixNano()))
	require.NoError(t, err)
	defer drop()
	_, err = Open(ctx, dsn)
	require.Error(t, err, "Open succeeded on a database with no schema")
	b, err := Open(ctx, dsn, MigrateOnOpen())
	require.NoError(t, err)
	_, err = b.pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) SELECT max(version_id) + 1, true FROM goose_db_version`)
	require.NoError(t, err)
	b.Close()
	b, err = Open(ctx, dsn)
	require.NoError(t, err, "Open refused a schema ahead of the binary")
	b.Close()
}

// Servers that migrate as they start together take turns, and all of them
// open.
func TestConcurrentMigrations(t *testing.T) {
	testBackend(t)
	ctx := t.Context()
	dsn, drop, err := createDatabase(ctx, baseDSN, fmt.Sprintf("milo_storage_concurrent_%d", time.Now().UnixNano()))
	require.NoError(t, err)
	defer drop()
	errs := make(chan error, 3)
	for range 3 {
		go func() {
			b, err := Open(ctx, dsn, MigrateOnOpen())
			if err == nil {
				b.Close()
			}
			errs <- err
		}()
	}
	for range 3 {
		require.NoError(t, <-errs)
	}
}

func TestOpenSetsConnectionLifetime(t *testing.T) {
	b := testBackend(t)
	require.Equal(t, connLifetime, b.pool.Config().MaxConnLifetime)
	require.Equal(t, connLifetimeJitter, b.pool.Config().MaxConnLifetimeJitter)

	u, err := url.Parse(testDBDSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("pool_max_conn_lifetime", "1h")
	u.RawQuery = q.Encode()
	own, err := Open(t.Context(), u.String())
	require.NoError(t, err)
	t.Cleanup(own.Close)
	require.Equal(t, time.Hour, own.pool.Config().MaxConnLifetime, "the DSN's lifetime wins")
}
