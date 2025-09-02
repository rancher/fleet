package errorutil

import (
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var ErrRetryable = errors.New("requeue event")

func IgnoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
