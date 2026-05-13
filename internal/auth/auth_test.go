package auth

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type staticSource struct{}

func (staticSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
}

func TestTokenSourceProvider(t *testing.T) {
	provider := NewTokenSourceProvider(staticSource{})
	token, err := provider.Token(context.Background())
	if err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if token != "token" {
		t.Fatalf("unexpected token: %s", token)
	}
}
