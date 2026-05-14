package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/oauth2/google"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
)

const CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// NewProvider creates a TokenProvider based on the auth configuration.
func NewProvider(ctx context.Context, cfg config.AuthConfig) (TokenProvider, error) {
	switch cfg.Type {
	case "none":
		return &noneProvider{}, nil
	case "bearer":
		return &bearerProvider{token: cfg.Token}, nil
	case "google_adc":
		return NewADCProvider(ctx)
	case "oauth_client_credentials":
		return newOAuthClientCredentialsProvider(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown auth type %q", cfg.Type)
	}
}

// noneProvider returns an empty token (no authentication).
type noneProvider struct{}

func (p *noneProvider) Token(_ context.Context) (string, error) { return "", nil }

// bearerProvider returns a static bearer token.
type bearerProvider struct{ token string }

func (p *bearerProvider) Token(_ context.Context) (string, error) { return p.token, nil }

// ADCProvider uses Google Application Default Credentials.
type ADCProvider struct {
	source oauth2.TokenSource
}

func NewADCProvider(ctx context.Context) (*ADCProvider, error) {
	creds, err := google.FindDefaultCredentials(ctx, CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to get application default credentials: %w", err)
	}
	return &ADCProvider{source: creds.TokenSource}, nil
}

func NewTokenSourceProvider(source oauth2.TokenSource) *ADCProvider {
	return &ADCProvider{source: source}
}

func (p *ADCProvider) Token(ctx context.Context) (string, error) {
	token, err := p.source.Token()
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}
	if !token.Valid() || token.AccessToken == "" {
		return "", fmt.Errorf("received invalid access token")
	}
	return token.AccessToken, nil
}

// oAuthClientCredentialsProvider obtains tokens via OAuth client credentials flow.
type oAuthClientCredentialsProvider struct {
	source oauth2.TokenSource
}

func newOAuthClientCredentialsProvider(ctx context.Context, cfg config.AuthConfig) (*oAuthClientCredentialsProvider, error) {
	if cfg.TokenURL == "" {
		return nil, fmt.Errorf("oauth_client_credentials: token_url is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("oauth_client_credentials: client_id is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("oauth_client_credentials: client_secret is required")
	}

	ccConfig := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}

	if cfg.InsecureSkipVerify {
		tlsClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // user-opt-in
			},
		}
		ctx = context.WithValue(ctx, oauth2.HTTPClient, tlsClient)
	}

	return &oAuthClientCredentialsProvider{source: ccConfig.TokenSource(ctx)}, nil
}

func (p *oAuthClientCredentialsProvider) Token(_ context.Context) (string, error) {
	token, err := p.source.Token()
	if err != nil {
		return "", fmt.Errorf("failed to get oauth token: %w", err)
	}
	if !token.Valid() || token.AccessToken == "" {
		return "", fmt.Errorf("received invalid oauth token")
	}
	return token.AccessToken, nil
}
