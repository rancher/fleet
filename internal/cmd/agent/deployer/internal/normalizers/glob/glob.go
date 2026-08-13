// +vendored argoproj/argo-cd/util/glob/glob.go
package glob

import (
	"fmt"

	"github.com/gobwas/glob"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func Match(pattern, text string, separators ...rune) bool {
	compiledGlob, err := glob.Compile(pattern, separators...)
	if err != nil {
		log.Log.Info(fmt.Sprintf("failed to compile pattern %s due to error %v", pattern, err))
		return false
	}
	return compiledGlob.Match(text)
}
