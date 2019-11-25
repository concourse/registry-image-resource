package resource

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
)

func RetryOnRateLimit(op func() error) error {
	bo := backoff.NewExponentialBackOff()
	if os.Getenv("TEST") == "true" {
		bo.InitialInterval = 5 * time.Millisecond
	} else {
		bo.InitialInterval = 5 * time.Second
	}
	bo.MaxInterval = 5 * time.Minute
	bo.MaxElapsedTime = 1 * time.Hour

	return backoff.RetryNotify(func() error {
		err := op()
		if err == nil {
			return nil
		}

		var transportErr *transport.Error
		if errors.As(err, &transportErr) {
			if transportErr.StatusCode == http.StatusTooManyRequests {
				return err
			}
		}

		return backoff.Permanent(err)
	}, bo, func(err error, dur time.Duration) {
		logrus.Warnf("too many requests; retrying in %s", dur)
	})
}
