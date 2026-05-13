package auth

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

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
