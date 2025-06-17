package githubapp

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestProvider_GetToken_CachingAndRefresh(t *testing.T) {
	origRT := http.DefaultTransport
	stubRT := &fakeRT{}
	http.DefaultTransport = stubRT
	t.Cleanup(func() { http.DefaultTransport = origRT })

	pemFile, err := os.CreateTemp(t.TempDir(), "ghapp*.pem")
	if err != nil {
		t.Fatalf("create temp pem: %v", err)
	}
	if _, err := pemFile.WriteString(testPEM); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	pemFile.Close()

	p := New(1, 2, pemFile.Name())

	ctx := context.Background()
	tok1, err := p.GetToken(ctx)
	if err != nil {
		t.Fatalf("first GetToken: %v", err)
	}
	if tok1 != "abc123" {
		t.Fatalf("unexpected token: %s", tok1)
	}
	if stubRT.calls != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", stubRT.calls)
	}

	tok2, err := p.GetToken(ctx)
	if err != nil {
		t.Fatalf("second GetToken: %v", err)
	}
	if tok2 != tok1 {
		t.Fatalf("cached token mismatch: %s", tok2)
	}
	if stubRT.calls != 1 {
		t.Fatalf("cache miss: HTTP calls = %d (want 1)", stubRT.calls)
	}

	// Force expiry and ensure provider refreshes the token
	p.mu.Lock()
	p.expiresAt = time.Now().Add(-time.Hour)
	p.mu.Unlock()

	if _, err := p.GetToken(ctx); err != nil {
		t.Fatalf("GetToken after manual expiry: %v", err)
	}
	if stubRT.calls != 2 {
		t.Fatalf("expected 2 HTTP calls after refresh, got %d", stubRT.calls)
	}
}
