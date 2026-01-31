package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func main() {
	clientID := os.Getenv("GMAIL_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_CLIENT_SECRET")
	email := os.Getenv("GMAIL_EMAIL")
	tokenFile := os.Getenv("OAUTH2_TOKEN_FILE")

	if clientID == "" || clientSecret == "" || email == "" {
		log.Fatal("GMAIL_CLIENT_ID, GMAIL_CLIENT_SECRET, and GMAIL_EMAIL must be set in env")
	}

	if tokenFile == "" {
		home, _ := os.UserHomeDir()
		tokenFile = filepath.Join(home, ".config", "archive_gmail", "token.json")
	}

	ctx := context.Background()
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"https://mail.google.com/"},
		Endpoint:     google.Endpoint,
		RedirectURL:  "http://localhost",
	}

	// Try loading an existing token
	token, err := loadToken(tokenFile)
	if err == nil && token.Valid() {
		fmt.Println("Loaded existing token. Attempting refresh if needed...")
	} else {
		fmt.Println("No valid token found, performing new OAuth2 login flow...")
		token = loginFlow(conf)
		saveToken(tokenFile, token)
		fmt.Printf("Token saved to %s\n", tokenFile)
	}

	// Refresh automatically if expired
	ts := conf.TokenSource(ctx, token)
	newToken, err := ts.Token()
	if err != nil {
		log.Fatalf("Failed to refresh token: %v", err)
	}

	if newToken.AccessToken != token.AccessToken {
		fmt.Println("Token refreshed, saving updated token...")
		saveToken(tokenFile, newToken)
	}

	fmt.Println("OAuth2 token ready for use!")
}

// loadToken reads a token from a file
func loadToken(file string) (*oauth2.Token, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// saveToken writes a token to a file
func saveToken(file string, token *oauth2.Token) {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		log.Fatalf("Failed to create token directory: %v", err)
	}
	data, _ := json.MarshalIndent(token, "", "  ")
	if err := os.WriteFile(file, data, 0600); err != nil {
		log.Fatalf("Failed to write token file: %v", err)
	}
}

// loginFlow runs the interactive OAuth2 login flow
func loginFlow(conf *oauth2.Config) *oauth2.Token {
	authURL := conf.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Println("\nOpen this URL in a browser, sign in, and copy the code from the redirect URL:")
	fmt.Println(authURL)
	fmt.Print("\nEnter code: ")

	var rawCode string
	fmt.Scanln(&rawCode)

	code, err := url.QueryUnescape(rawCode)
	if err != nil {
		log.Fatalf("Failed to decode code: %v", err)
	}

	ctx := context.Background()
	token, err := conf.Exchange(ctx, code)
	if err != nil {
		log.Fatalf("Failed to exchange code for token: %v", err)
	}

	// Set expiry buffer in case of clock skew
	if token.Expiry.IsZero() {
		token.Expiry = time.Now().Add(time.Hour)
	}

	return token
}
