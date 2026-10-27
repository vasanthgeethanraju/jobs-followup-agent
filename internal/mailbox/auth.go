// Package mailbox talks to Gmail: logging in (OAuth) and reading/drafting emails.
package mailbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Scopes = the permissions we ask the user for.
var scopes = []string{
	gmail.GmailReadonlyScope, // read !Jobs emails and check replies
	gmail.GmailComposeScope,  // create follow-up drafts
}

// NewService logs in (re-using token.json if we have one) and returns a Gmail API client.
//
// Note the return type "(*gmail.Service, error)": Go functions often return
// TWO values — the result and an error. There are no exceptions in Go;
// the caller checks "if err != nil" instead.
func NewService(ctx context.Context, credentialsPath, tokenPath string) (*gmail.Service, error) {
	credBytes, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", credentialsPath, err)
	}

	// Build an OAuth config from credentials.json (client ID + secret).
	config, err := google.ConfigFromJSON(credBytes, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	// Try the saved token first so you only log in through the browser once.
	tok, err := loadToken(tokenPath)
	if err != nil {
		tok, err = loginWithBrowser(ctx, config)
		if err != nil {
			return nil, err
		}
		if err := saveToken(tokenPath, tok); err != nil {
			return nil, err
		}
	}

	// config.Client returns an HTTP client that attaches the token to every
	// request and refreshes it automatically when it expires.
	httpClient := config.Client(ctx, tok)
	return gmail.NewService(ctx, option.WithHTTPClient(httpClient))
}

// loginWithBrowser runs the OAuth "loopback" flow:
//  1. start a tiny web server on localhost
//  2. open Google's login page in your browser
//  3. Google redirects back to our server with a one-time "code"
//  4. we exchange that code for an access token + refresh token
func loginWithBrowser(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	// Port 0 = "pick any free port".
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start local server: %w", err)
	}
	defer listener.Close() // "defer" runs this when the function returns

	port := listener.Addr().(*net.TCPAddr).Port
	config.RedirectURL = fmt.Sprintf("http://localhost:%d", port)

	// "state" is a random value that protects against forged redirects (CSRF).
	state := randomString()

	// A channel is how goroutines (lightweight threads) pass values to each other.
	// The web server sends the code into it; this function waits to receive it.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth state mismatch")
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			fmt.Fprintln(w, "Login failed:", e)
			errCh <- fmt.Errorf("oauth error: %s", e)
			return
		}
		fmt.Fprintln(w, "Logged in! You can close this tab and go back to the terminal.")
		codeCh <- r.URL.Query().Get("code")
	})}

	// "go" starts the server in the background (a goroutine).
	go server.Serve(listener)
	defer server.Close()

	// AccessTypeOffline asks for a refresh token so we stay logged in.
	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Println("Opening your browser to log in to Google...")
	fmt.Println("If it doesn't open, visit this URL:\n" + authURL)
	_ = exec.Command("open", authURL).Start() // macOS "open"; ignore errors

	// "select" waits on several channels at once and takes whichever comes first.
	select {
	case code := <-codeCh:
		return config.Exchange(ctx, code)
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func loadToken(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

func saveToken(path string, tok *oauth2.Token) error {
	// 0600 = only your user can read/write this file (it's a live Gmail login).
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(tok)
}

func randomString() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
