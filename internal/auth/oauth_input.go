package auth

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/markbates/goth"
)

var errInvalidOAuthInput = errors.New("invalid OAuth input")

// oauthRequestForPath validates the provider selected by the route, parses the
// complete raw query, and makes that route value authoritative for Goth.
func oauthRequestForPath(request *http.Request, routeProvider string) (*http.Request, goth.Provider, url.Values, error) {
	if routeProvider != "google" && routeProvider != "github" {
		return nil, nil, nil, errInvalidOAuthInput
	}
	provider, err := goth.GetProvider(routeProvider)
	if err != nil {
		return nil, nil, nil, errInvalidOAuthInput
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, nil, nil, errInvalidOAuthInput
	}
	query.Del("provider")
	query.Del(":provider")
	query.Set("provider", routeProvider)

	cloned := request.Clone(request.Context())
	cloned.URL.RawQuery = query.Encode()
	return cloned, provider, query, nil
}

func uniqueNonBlankOAuthState(query url.Values) (string, bool) {
	states, ok := query["state"]
	if !ok || len(states) != 1 || strings.TrimSpace(states[0]) == "" {
		return "", false
	}
	return states[0], true
}
