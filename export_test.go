package resource

import (
	"net/http"
	"time"
)

// Exported aliases for internal functions, used by azure_test.go (package resource_test).
// This is the standard Go pattern for white-box testing from an external test package.

var ParseACRChallengeTenant = parseACRChallengeTenant
var ExchangeACRRefreshToken = exchangeACRRefreshToken
var AcrChallengeTenant = acrChallengeTenant
var NewACRHTTPClient = newACRHTTPClient
var ResolveAzureCloud = resolveAzureCloud

// PlainHTTPClient is a test helper client used for plain-HTTP test servers.
var PlainHTTPClient = &http.Client{Timeout: 5 * time.Second}
