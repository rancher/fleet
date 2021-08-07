package cluster

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/yaml"
)

func TestTokenFinder(t *testing.T) {
	a := assert.New(t)
	expectedToken := "u-foo:bar"
	value := "YXBpVmVyc2lvbjogdjEKY2x1c3RlcnM6Ci0gY2x1c3RlcjoKICAgIGNlcnRpZmljYXRlLWF1dGhvcml0eS1kYXRhOiBMUzB0TFMxQ1JVZEpUaUJEUlZKVVNVWkpRMEZVUlMwdExTMHRDbVp2YjJKaGNnb3RMUzB0TFVWT1JDQkRSVkpVU1VaSlEwRlVSUzB0TFMwdAogICAgc2VydmVyOiBodHRwczovL2Zvby5iYXIvazhzL2NsdXN0ZXJzL2xvY2FsCiAgbmFtZTogY2x1c3Rlcgpjb250ZXh0czoKLSBjb250ZXh0OgogICAgY2x1c3RlcjogY2x1c3RlcgogICAgdXNlcjogdXNlcgogIG5hbWU6IGRlZmF1bHQKY3VycmVudC1jb250ZXh0OiBkZWZhdWx0CmtpbmQ6IENvbmZpZwpwcmVmZXJlbmNlczoge30KdXNlcnM6Ci0gbmFtZTogdXNlcgogIHVzZXI6CiAgICB0b2tlbjogdS1mb286YmFy"
	decoded, err := base64.StdEncoding.DecodeString(value)
	a.NoError(err)
	m := make(map[string]interface{})
	err = yaml.Unmarshal(decoded, &m)
	a.NoError(err)

	token := ""
	for _, user := range m["users"].([]interface{}) {
		if user.(map[string]interface{})["name"] == "user" {
			token = user.(map[string]interface{})["user"].(map[string]interface{})["token"].(string)
			break
		}
	}
	a.Equal(token, expectedToken)
}
