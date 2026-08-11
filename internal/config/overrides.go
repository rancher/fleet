package config

import (
	"fmt"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	envVarSSLCertFile = "SSL_CERT_FILE"
	envVarSSLCertDir  = "SSL_CERT_DIR"
)

// BypassSystemCAStore is used to bypass the OS trust store in agents through env vars, see
// https://pkg.go.dev/crypto/x509#SystemCertPool for more info.
// We set values to paths belonging to the root filesystem, which is read-only, to prevent tampering.
// Eventually, this should not be necessary, if/when we find a way to set client-go's API Config to achieve similar
// effects.
// Note: this will not work on Windows nor Mac OS. Agents are expected to run on Linux nodes.
// Returns a function allowing the bypass to be undone.
func BypassSystemCAStore() func() {
	certFileBkp := os.Getenv(envVarSSLCertFile)
	certDirBkp := os.Getenv(envVarSSLCertDir)

	if err := os.Setenv(envVarSSLCertFile, "/dev/null"); err != nil {
		log.Log.Error(nil, fmt.Sprintf("failed to set env var %s: %s", envVarSSLCertFile, err.Error()))
	}

	if err := os.Setenv(envVarSSLCertDir, "/dev/null"); err != nil {
		log.Log.Error(nil, fmt.Sprintf("failed to set env var %s: %s", envVarSSLCertDir, err.Error()))
	}

	return func() {
		if err := os.Setenv(envVarSSLCertFile, certFileBkp); err != nil {
			log.Log.Error(nil, fmt.Sprintf("failed to restore env var %s: %s", envVarSSLCertFile, err.Error()))
		}

		if err := os.Setenv(envVarSSLCertDir, certDirBkp); err != nil {
			log.Log.Error(nil, fmt.Sprintf("failed to restore env var %s: %s", envVarSSLCertDir, err.Error()))
		}
	}
}
