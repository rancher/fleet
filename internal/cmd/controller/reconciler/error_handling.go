package reconciler

import (
	"errors"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/pkg/durations"

	"github.com/rancher/wrangler/v3/pkg/condition"
)

type copyable[T any] interface {
	DeepCopy() T
}

// CheckRetryable checks if err is retryable; if so, it returns `true`, along with a controller result triggering a
// requeue after the default duration.
// If err is non-retryable, it simply returns `false` with empty/nil values.
func CheckRetryable(err error, logger logr.Logger) (bool, ctrl.Result, error) {
	if errors.Is(err, fleetutil.ErrRetryable) {
		logger.Info(err.Error())
		return true, ctrl.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
	}

	return false, ctrl.Result{}, nil
}

// SetCondition sets the condition and updates the timestamp, if the condition changed
func SetCondition[T any](cond string, s copyable[T], err error) {
	c := condition.Cond(cond)
	origStatus := s.DeepCopy()

	c.SetError(s, "", fleetutil.IgnoreConflict(err))

	if !equality.Semantic.DeepEqual(origStatus, s) {
		c.LastUpdated(s, time.Now().UTC().Format(time.RFC3339))
	}
}
