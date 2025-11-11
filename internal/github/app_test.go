package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const (
	validRSA = `-----BEGIN RSA PRIVATE KEY-----
MIICXQIBAAKBgQC1ZuFGlFeAFqeS6p04QsliOXG3NH1/lQC4UMXdQ0F73ciYBPKq
iQZcoyOu8a2Hsi5HvxDqR1rreTAkJ37C3ErrmKcE1CUJwxBVqkgE17Fzw63QBu0X
0OVtaUarG8Pd9HuKbXPK8HXFTEh6F5hoqmzCmG7cRHmagBeh1SqZm1awzQIDAQAB
AoGAChHZ84cMjGm1h6xKafMbJr61l0vso4Zr8c9aDHxNSEj5d6beqaTNm5rawj1c
Oqojc4whrj+jxmqFx5wBp2N/LRi7GhpPco4wy8gg2t/OjmcR+jTRJgT1x1Co9W58
U+O5c001YFTNoa1UUUBweqye/sX/k5GBCUt0V2G839Cn+8ECQQD2K2eZcyUeeBHT
/YhGAq++mmfVEkzMY7U+G59oeF038zXX+wtMwoKmC9/LHwVPWpnzL/oMu3zZqv4a
jzCOAdZpAkEAvKVas8KUctHUBvDoU6hq9bVyIZMZZnlBfysuFEeJLU8efp/n4KRO
93EyhcXe2FmOC/VzGbkiQobmAqVvIwTixQJBAIKYZE20GG0hpdOhHTqHElU79PnE
y5ljDDP204rI0Ctui5IZTNVcG5ObmQ5ZVqfSmPm66hz3GjUf0c6lSE0ODIECQHB0
silO6We5JggtPJICaCCpVawmIJIx3pWMjB+StXfJHoilknkb+ecQF+ofFsUqPb9r
Rn4jGwVFnYAeVq4tj3ECQQCyeMeCprz5AQ8HSd16Asd3zhv7N7olpb4XMIP6YZXy
udiSlDctMM/X3ZM2JN5M1rtAJ2WR3ZQtmWbOjZAbG2Eq
-----END RSA PRIVATE KEY-----`
	invalidRSA = `-----BEGIN RSA PRIVATE KEY-----
AQIDBA==
-----END RSA PRIVATE KEY-----`
	notRSA = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg3rAS658JOtxkOQ4L
7n8EebUpsbeV9Kx/iFGXwxjHPUOhRANCAAQCidzm5b6x5dXdMuq3b7sL52FdqkWx
ytV/UsL9lo9CSv5UTTAnRAjZkyFjDO3cieDA322H+5VQKI7moiKsfz6p
-----END PRIVATE KEY-----`
)

type fakeRT struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	body := `{"token":"abc123","expires_at":"2100-01-01T00:00:00Z"}`
	return &http.Response{
		StatusCode: http.StatusCreated,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestGitHubApp_GetToken_Success(t *testing.T) {
	orig := http.DefaultTransport
	stub := &fakeRT{}
	http.DefaultTransport = stub
	t.Cleanup(func() { http.DefaultTransport = orig })

	app := NewApp(123, 456, []byte(validRSA))

	token, err := app.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken returned error: %v", err)
	}
	if token != "abc123" {
		t.Fatalf("unexpected token %q (want %q)", token, "abc123")
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly one outbound HTTP request, got %d", stub.calls)
	}
}

func TestGitHubApp_GetToken_InvalidPEM(t *testing.T) {
	app := NewApp(123, 456, []byte("definitely-not-a-PEM-block"))

	_, err := app.GetToken(context.Background())
	if err == nil {
		t.Fatalf("expected error for invalid PEM, got nil")
	}
	const want = "pem decode failed for app"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestGitHubApp_GetToken_NotRSA(t *testing.T) {
	app := NewApp(123, 456, []byte(notRSA))

	_, err := app.GetToken(context.Background())
	if err == nil {
		t.Fatalf("expected error for not RSA PEM, got nil")
	}
	const want = "unsupported key type"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestGitHubApp_GetToken_InvalidRSA(t *testing.T) {
	app := NewApp(123, 456, []byte(invalidRSA))

	_, err := app.GetToken(context.Background())
	if err == nil {
		t.Fatalf("expected error for not RSA PEM, got nil")
	}
	const want = "invalid RSA key for app"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}
