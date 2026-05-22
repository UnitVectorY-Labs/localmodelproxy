package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
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

func TestOAuthClientCredentialsAllowsInsecureSkipVerify(t *testing.T) {
	tokenServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "grant_type=client_credentials") {
			t.Fatalf("unexpected token request body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tls-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	provider, err := newOAuthClientCredentialsProvider(context.Background(), config.AuthConfig{
		Type:               "oauth_client_credentials",
		TokenURL:           tokenServer.URL,
		ClientID:           "client",
		ClientSecret:       "secret",
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("newOAuthClientCredentialsProvider returned error: %v", err)
	}

	token, err := provider.Token(context.Background())
	if err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if token != "tls-token" {
		t.Fatalf("unexpected token: %s", token)
	}
}
