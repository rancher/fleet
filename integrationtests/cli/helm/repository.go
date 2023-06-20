package helm

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/rancher/fleet/integrationtests/cli"
)

const (
	username = "user"
	password = "pass"
)

type repository struct {
	server *http.Server
	port   string
}

// starts a helm repository on localhost:3000. It contains all repositories that are located in the assets/helmrepository folder.
// basic auth is enabled is authEnabled is true.
func (r *repository) startRepository(authEnabled bool) {
	r.server = &http.Server{Addr: r.port, ReadHeaderTimeout: 1 * time.Second}
	r.server.Handler = getHandler(authEnabled)
	go func() {
		const maxAttempts = 10
		currentAttempt := 0
		for {
			err := r.server.ListenAndServe()
			// It is possible that the previous repository is still closing as it takes a few extra milliseconds to fully close
			// retry after 100 milliseconds if "address already in use" error is returned
			if err != nil && strings.Contains(err.Error(), "address already in use") {
				if currentAttempt == maxAttempts {
					panic("Max number of attempts reached: error creating helm repository: " + err.Error())
				}
				currentAttempt++
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if err != nil && err != http.ErrServerClosed {
				panic("error creating helm repository: " + err.Error())
			}
			break
		}
	}()
}

func getHandler(authEnabled bool) http.Handler {
	fs := http.FileServer(http.Dir(cli.AssetsPath + "helmrepository"))
	if !authEnabled {
		return fs
	}
	return &authHandler{fs: fs}
}

func (r *repository) stopRepository() error {
	return r.server.Close()
}

// handler with basic authentication enabled
type authHandler struct {
	fs http.Handler
}

func (h *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(user)), []byte(username)) != 1 || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(pass)), []byte(password)) != 1 {
		w.WriteHeader(401)
		_, err := w.Write([]byte("Unauthorised."))
		if err != nil {
			return
		}
		return
	}
	h.fs.ServeHTTP(w, r)
}
