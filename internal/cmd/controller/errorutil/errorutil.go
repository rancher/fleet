package errorutil

import (
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var ErrRetryable = errors.New("requeue event")

// ErrHashMismatch is returned when the BD options secret content does not match the BD's
// ValuesHash. It signals that the secret was updated but the BD spec was not (or vice-versa),
// leaving them inconsistent. The bundle controller self-heals by clearing ValuesHash and
// requeuing so the next reconcile can re-sync the secret.
var ErrHashMismatch = errors.New("hash mismatch between secret and bundledeployment")

func IgnoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
