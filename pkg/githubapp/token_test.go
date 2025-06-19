package githubapp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const testPEM = `-----BEGIN RSA PRIVATE KEY-----
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
		StatusCode: 201,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestGitHubApp_GetToken_Success(t *testing.T) {
	orig := http.DefaultTransport
	stub := &fakeRT{}
	http.DefaultTransport = stub
	t.Cleanup(func() { http.DefaultTransport = orig })

	app := NewApp(1, 2, []byte(testPEM))

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
	app := NewApp(10, 20, []byte("definitely-not-a-PEM-block"))

	_, err := app.GetToken(context.Background())
	if err == nil {
		t.Fatalf("expected error for invalid PEM, got nil")
	}
	const want = "does not have a valid private key"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}
