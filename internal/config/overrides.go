package config

import (
	"os"

	"github.com/sirupsen/logrus"
)

// BypassSystemCAStore is used to bypass the OS trust store in agents through env vars, see
// https://pkg.go.dev/crypto/x509#SystemCertPool for more info.
// We set values to paths belonging to the root filesystem, which is read-only, to prevent tampering.
// Eventually, this should not be necessary, if/when we find a way to set client-go's API Config to achieve similar
// effects.
// Note: this will not work on Windows nor Mac OS. Agents are expected to run on Linux nodes.
func BypassSystemCAStore() {
	err := os.Setenv("SSL_CERT_FILE", "/dev/null")
	if err != nil {
		logrus.Errorf("failed to set env var SSL_CERT_FILE: %s", err.Error())
	}

	err = os.Setenv("SSL_CERT_DIR", "/dev/null")
	if err != nil {
		logrus.Errorf("failed to set env var SSL_CERT_DIR: %s", err.Error())
	}
}
