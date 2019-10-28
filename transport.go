package resource

import (
	"context"
	"net/http"

	"github.com/hashicorp/go-retryablehttp"
)

func RetryTransport() http.RoundTripper {
	client := retryablehttp.NewClient()
	client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if err == nil && resp.StatusCode == 429 {
			return true, nil
		}
		return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	}
	return &retryableTransport{client}
}

type retryableTransport struct {
	*retryablehttp.Client
}

func (c *retryableTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	req, err := retryablehttp.FromRequest(request)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}
