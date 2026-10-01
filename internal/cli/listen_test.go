package cli

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeHTTPLogsThePickedPort(t *testing.T) {
	addr, line := serveOnPickedPort(t, "", "")
	assertServed(t, http.DefaultClient, line, "http://"+addr)
}

func TestServeHTTPLogsThePickedPortWithTLS(t *testing.T) {
	certPEM, certFile, keyFile := writeSelfSignedCert(t)
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("the generated cert does not parse")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	defer client.CloseIdleConnections()

	addr, line := serveOnPickedPort(t, certFile, keyFile)
	assertServed(t, client, line, "https://"+addr)
}

func serveOnPickedPort(t *testing.T, certFile, keyFile string) (addr, line string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { reader.Close() })
	output, flags := log.Writer(), log.Flags()
	t.Cleanup(func() { log.SetOutput(output); log.SetFlags(flags) })
	log.SetOutput(writer)
	log.SetFlags(0)

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	t.Cleanup(func() { server.Close() })
	go serveHTTP(server, listener, certFile, keyFile)

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		lines <- strings.TrimSpace(line)
	}()
	select {
	case line = <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("no listening line was logged")
	}
	return listener.Addr().String(), line
}

func assertServed(t *testing.T, client *http.Client, line, wantURL string) {
	t.Helper()
	if want := "listening on " + wantURL; line != want {
		t.Fatalf("logged %q, want %q", line, want)
	}
	if strings.HasSuffix(line, ":0") {
		t.Fatalf("logged the unresolved port: %q", line)
	}
	response, err := client.Get(wantURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d at the logged address", response.StatusCode)
	}
}

func writeSelfSignedCert(t *testing.T) (certPEM []byte, certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,

		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPEM, certFile, keyFile
}
