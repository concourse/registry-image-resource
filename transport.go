package resource

import (
	"net/http"
	"time"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/retryhttp"
)

var RetryTransport = &retryhttp.RetryRoundTripper{
	Logger:         &discardLogger{},
	BackOffFactory: retryhttp.NewExponentialBackOffFactory(10 * time.Minute),
	RoundTripper:   http.DefaultTransport,
	Retryer:        &retryhttp.DefaultRetryer{},
}

// discardLogger is an inert logger.
type discardLogger struct{}

func (*discardLogger) Debug(string, ...lager.Data)                  {}
func (*discardLogger) Info(string, ...lager.Data)                   {}
func (*discardLogger) Error(string, error, ...lager.Data)           {}
func (*discardLogger) Fatal(string, error, ...lager.Data)           {}
func (*discardLogger) RegisterSink(lager.Sink)                      {}
func (*discardLogger) SessionName() string                          { return "" }
func (d *discardLogger) Session(string, ...lager.Data) lager.Logger { return d }
func (d *discardLogger) WithData(lager.Data) lager.Logger           { return d }
