package hooks

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rancher/gitjob/pkg/provider"
	"github.com/rancher/gitjob/pkg/provider/github"
	"github.com/rancher/gitjob/pkg/types"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/json"
)

type WebhookHandler struct {
	providers []provider.Provider
}

func newHandler(rContext *types.Context) *WebhookHandler {
	wh := &WebhookHandler{
		providers: []provider.Provider{
			// register all supported webhook handler here
			github.NewGitHub(rContext.Gitjob.Gitjob().V1().GitJob()),
		},
	}

	return wh
}

func (h *WebhookHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	code, err := h.execute(req)
	if err != nil {
		e := map[string]interface{}{
			"type":    "error",
			"code":    code,
			"message": err.Error(),
		}
		logrus.Debugf("executing webhook request got error: %v", err)
		rw.WriteHeader(code)
		responseBody, err := json.Marshal(e)
		if err != nil {
			logrus.Errorf("Failed to unmarshall response, error: %v", err)
		}
		_, err = rw.Write(responseBody)
		if err != nil {
			logrus.Errorf("Failed to write response, error: %v", err)
		}
	}
}

func (h *WebhookHandler) execute(req *http.Request) (int, error) {
	for _, provider := range h.providers {
		code, err := provider.HandleHook(req.Context(), req)
		if err != nil {
			return code, err
		}
		return code, nil
	}
	return http.StatusNotFound, fmt.Errorf("unknown provider")
}

func HandleHooks(ctx *types.Context) http.Handler {
	root := mux.NewRouter()
	hooksHandler := newHandler(ctx)
	root.UseEncodedPath()
	root.PathPrefix("/hooks").Handler(hooksHandler)
	return root
}
