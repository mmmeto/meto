package auth

import (
	"bufio"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFirebaseVerification(t *testing.T) {
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", "")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "meto-test"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	certificates, err := json.Marshal(map[string]string{"test-key": cert})
	if err != nil {
		t.Fatal(err)
	}
	// Only the SDK's public certificate fetch is replaced; JWT verification remains real.
	previous := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		DialTLSContext: func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "www.googleapis.com:443" {
				return nil, errors.New("unexpected outbound request in token test")
			}
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				_ = server.SetDeadline(time.Now().Add(5 * time.Second))
				request, err := http.ReadRequest(bufio.NewReader(server))
				if err != nil {
					return
				}
				if !strings.Contains(request.URL.Path, "securetoken@system.gserviceaccount.com") {
					return
				}
				response := &http.Response{
					StatusCode: http.StatusOK, ProtoMajor: 1, ProtoMinor: 1,
					Header:        http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"max-age=3600"}},
					Body:          io.NopCloser(strings.NewReader(string(certificates))),
					ContentLength: int64(len(certificates)), Close: true,
				}
				_ = response.Write(server)
			}()
			return client, nil
		},
	}
	t.Cleanup(func() { http.DefaultTransport = previous })
	verifier, err := NewFirebaseVerifier(context.Background(), "demo-meto-test")
	if err != nil {
		t.Fatal(err)
	}

	claims := func() map[string]interface{} {
		return map[string]interface{}{
			"sub": "verified-user", "aud": "demo-meto-test",
			"iss": "https://securetoken.google.com/demo-meto-test",
			"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
			"auth_time": now.Add(-time.Minute).Unix(),
			"name":      "Verified Name", "email": "user@example.test",
			"picture":  "https://example.test/avatar.png",
			"firebase": map[string]interface{}{"sign_in_provider": "google.com"},
		}
	}
	rawToken := signTestToken(t, key, claims())
	if _, err := verifier.client.VerifyIDToken(context.Background(), rawToken); err != nil {
		t.Fatalf("SDK verification of test token: %v", err)
	}
	identity, err := verifier.Verify(context.Background(), rawToken)
	if err != nil || identity.ID != "verified-user" || identity.DisplayName != "Verified Name" || identity.Email == nil || *identity.Email != "user@example.test" {
		t.Fatalf("valid signature rejected or claims mismatched: %+v %v", identity, err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{"wrong audience", func(c map[string]interface{}) { c["aud"] = "another-project" }},
		{"wrong issuer", func(c map[string]interface{}) { c["iss"] = "https://attacker.test" }},
		{"expired", func(c map[string]interface{}) { c["exp"] = now.Add(-time.Hour).Unix() }},
		{"future issued", func(c map[string]interface{}) { c["iat"] = now.Add(time.Hour).Unix() }},
		{"missing subject", func(c map[string]interface{}) { delete(c, "sub") }},
		{"tenant token", func(c map[string]interface{}) { c["firebase"] = map[string]interface{}{"tenant": "other-tenant"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := claims()
			tt.mutate(input)
			if _, err := verifier.Verify(context.Background(), signTestToken(t, key, input)); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected invalid token, got %v", err)
			}
		})
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), signTestToken(t, otherKey, claims())); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged signature accepted: %v", err)
	}

	http.DefaultTransport = &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("certificate endpoint unavailable")
		},
	}
	unavailable, err := NewFirebaseVerifier(context.Background(), "demo-meto-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unavailable.Verify(context.Background(), rawToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("certificate fetch failure must be unavailable, got %v", err)
	}
}

func TestFirebaseRefusesEmulatorMode(t *testing.T) {
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", "127.0.0.1:9099")
	if _, err := NewFirebaseVerifier(context.Background(), "demo-meto-test"); err == nil {
		t.Fatal("unsigned emulator mode must be rejected")
	}
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]interface{}) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}
