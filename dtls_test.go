package glwm2m

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety"
	"github.com/maxid/gnety/codec/coap"
)

func dtlsCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)
	return certFile, keyFile
}

func TestRegistrationOverDTLS(t *testing.T) {
	certFile, keyFile := dtlsCert(t)
	srv, err := New(Config{Addr: "127.0.0.1:0", Network: "dtls", TLS: gnety.TLSConfig{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())
	cli, err := gcoap.DialConfig(context.Background(), gcoap.ClientConfig{
		Addr: srv.Addr(), Network: "dtls", TLS: gnety.TLSConfig{CAFile: certFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := cli.Do(ctx, gcoap.ClientRequest{
		Code:  coap.CodePOST,
		Path:  "/rd",
		Query: map[string][]string{"ep": {"dev-dtls"}, "lt": {"60"}, "b": {"U"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Code != coap.CodeCreated {
		t.Fatalf("code = %v", resp.Code)
	}
	if srv.Count() != 1 {
		t.Fatalf("count = %d", srv.Count())
	}
}
