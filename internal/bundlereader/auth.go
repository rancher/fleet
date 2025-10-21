package bundlereader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Auth struct {
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	CABundle           []byte `json:"caBundle,omitempty"`
	SSHPrivateKey      []byte `json:"sshPrivateKey,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
	BasicHTTP          bool   `json:"basicHTTP,omitempty"`
	// remember to update Hash() when adding/modifying fields
}

var nullBytes = []byte("\x00")

func (a Auth) Hash() string {
	hash := sha256.New()
	for _, v := range [][]byte{
		[]byte(a.Username),
		[]byte(a.Password),
		a.CABundle,
		a.SSHPrivateKey,
		{toByte(a.InsecureSkipVerify)},
		{toByte(a.BasicHTTP)},
	} {
		hash.Write(v)
		hash.Write(nullBytes)
	}
	return hex.EncodeToString(hash.Sum(nil))

}

func toByte(v bool) byte {
	if v {
		return byte(1)
	}
	return byte(0)
}

func ReadHelmAuthFromSecret(ctx context.Context, c client.Reader, req types.NamespacedName) (Auth, error) {
	if req.Name == "" {
		return Auth{}, nil
	}
	secret := &corev1.Secret{}
	err := c.Get(ctx, req, secret)
	if err != nil {
		return Auth{}, err
	}

	auth := Auth{}
	username, okUsername := secret.Data[corev1.BasicAuthUsernameKey]
	if okUsername {
		auth.Username = string(username)
	}

	password, okPasswd := secret.Data[corev1.BasicAuthPasswordKey]
	if okPasswd {
		auth.Password = string(password)
	}

	// check that username and password are both set or none is set
	if okUsername && !okPasswd {
		return Auth{}, fmt.Errorf("%s is set in the secret, but %s isn't", corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey)
	} else if !okUsername && okPasswd {
		return Auth{}, fmt.Errorf("%s is set in the secret, but %s isn't", corev1.BasicAuthPasswordKey, corev1.BasicAuthUsernameKey)
	}

	caBundle, ok := secret.Data["cacerts"]
	if ok {
		auth.CABundle = caBundle
	}

	// Get the values for skipping TLS and basic HTTP connections.
	// In case of error reading the values they will be considered
	// as set to false as those values are security related.
	insecureSkipVerify := false
	if value, ok := secret.Data["insecureSkipVerify"]; ok {
		boolValue, err := strconv.ParseBool(string(value))
		if err == nil {
			insecureSkipVerify = boolValue
		}
	}

	basicHTTP := false
	if value, ok := secret.Data["basicHTTP"]; ok {
		boolValue, err := strconv.ParseBool(string(value))
		if err == nil {
			basicHTTP = boolValue
		}
	}

	auth.InsecureSkipVerify = insecureSkipVerify
	auth.BasicHTTP = basicHTTP

	return auth, nil
}
