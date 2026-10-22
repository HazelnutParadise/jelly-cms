package auth

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

const oauthProviderRequestTimeout = 30 * time.Second

func fetchOAuthUser(parent context.Context, provider goth.Provider, session goth.Session, params goth.Params) (goth.User, error) {
	ctx, cancel := context.WithTimeout(parent, oauthProviderRequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return goth.User{}, err
	}

	requestProvider := providerWithOAuthContext(ctx, provider)
	if _, err := session.Authorize(requestProvider, params); err != nil {
		return goth.User{}, err
	}
	if err := ctx.Err(); err != nil {
		return goth.User{}, err
	}
	user, err := requestProvider.FetchUser(session)
	if err != nil {
		return goth.User{}, err
	}
	if err := ctx.Err(); err != nil {
		return goth.User{}, err
	}
	return user, nil
}

func providerWithOAuthContext(ctx context.Context, provider goth.Provider) goth.Provider {
	switch p := provider.(type) {
	case *google.Provider:
		copy := *p
		copy.HTTPClient = clientWithOAuthContext(ctx, p.Client())
		return &copy
	case *github.Provider:
		copy := *p
		copy.HTTPClient = clientWithOAuthContext(ctx, p.Client())
		return &copy
	default:
		return provider
	}
}

func clientWithOAuthContext(ctx context.Context, client *http.Client) *http.Client {
	copy := *client
	transport := copy.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copy.Transport = &oauthContextTransport{ctx: ctx, base: transport}
	return &copy
}

type oauthContextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t *oauthContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		stop()
		cancel()
		return resp, err
	}
	if resp == nil || resp.Body == nil {
		stop()
		cancel()
		return resp, nil
	}
	resp.Body = &oauthContextBody{ReadCloser: resp.Body, stop: stop, cancel: cancel}
	return resp, nil
}

type oauthContextBody struct {
	io.ReadCloser
	stop   func() bool
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (b *oauthContextBody) Close() error {
	b.once.Do(func() {
		b.stop()
		b.cancel()
		b.err = b.ReadCloser.Close()
	})
	return b.err
}
