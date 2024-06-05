package errorutil

import apierrors "k8s.io/apimachinery/pkg/api/errors"

func IgnoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
