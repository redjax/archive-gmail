package gmailService

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	config "github.com/redjax/archive-gmail/internal/config"
	"github.com/redjax/archive-gmail/internal/utils"
)

// DrainChannel drains a channel for up to maxWait
func DrainChannel[T any](ch <-chan T, maxWait time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-ticker.C:
		}
	}
}

// ListMailboxes returns all selectable mailboxes
func ListMailboxes(c *client.Client) ([]string, error) {
	ch := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)

	go func() { done <- c.List("", "*", ch) }()

	var boxes []string
	for m := range ch {
		skip := false
		for _, a := range m.Attributes {
			if a == imap.NoSelectAttr {
				skip = true
				break
			}
		}
		if !skip {
			boxes = append(boxes, m.Name)
		}
	}

	return boxes, <-done
}

// MailboxDir returns the filesystem-safe path for a mailbox
func MailboxDir(base, box string) string {
	safe := strings.ReplaceAll(box, "/", "_")
	return filepath.Join(base, safe)
}

// MessagePath returns the path for a message file
func MessagePath(base, box string, msgID uint64) string {
	return filepath.Join(MailboxDir(base, box), fmt.Sprintf("%d.eml", msgID))
}

// Connect connects to IMAP using either password or OAuth2
func Connect(cfg config.Config) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.ImapServer, cfg.ImapPort)
	tlsCfg := &tls.Config{
		ServerName:         cfg.ImapServer,
		InsecureSkipVerify: cfg.TLSSkipVerify,
	}

	c, err := client.DialTLS(addr, tlsCfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	c.Timeout = 5 * time.Minute

	if cfg.ClientID != "" && cfg.ClientSecret != "" {
		logrus.Info("Using OAuth2")
		if err := authenticateOAuth2(c, cfg); err != nil {
			return nil, err
		}
		return c, nil
	}

	logrus.Info("Using app password")
	if err := c.Login(cfg.Email, cfg.Password); err != nil {
		return nil, err
	}

	return c, nil
}

// ProcessMailbox downloads missing messages from a mailbox
func ProcessMailbox(c *client.Client, box string, cfg config.Config, downloaded *uint64, mu *sync.RWMutex) {
	logrus.Infof("Processing: %s", box)

	var mboxStatus *imap.MailboxStatus
	var selectErr error
	for retry := 0; retry < 3; retry++ {
		mboxStatus, selectErr = c.Select(box, true)
		if selectErr == nil {
			break
		}
		time.Sleep(time.Duration(retry+1) * time.Second)
	}

	if selectErr != nil || mboxStatus == nil || mboxStatus.Messages == 0 {
		logrus.Infof("Skipping mailbox %s: empty or select failed", box)
		return
	}

	if err := utils.EnsureDir(MailboxDir(cfg.BackupDir, box), cfg.DryRun); err != nil {
		logrus.Warnf("Failed to create mailbox dir: %v", err)
		return
	}

	uidSeq := new(imap.SeqSet)
	uidSeq.AddRange(1, mboxStatus.UidNext-1)
	uidMsgs := make(chan *imap.Message, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fetchErr := make(chan error, 1)
	go func() { fetchErr <- c.UidFetch(uidSeq, []imap.FetchItem{imap.FetchUid}, uidMsgs) }()

	missingUIDs := make([]uint32, 0)
loop:
	for {
		select {
		case msg, ok := <-uidMsgs:
			if !ok {
				break loop
			}
			path := MessagePath(cfg.BackupDir, box, uint64(msg.Uid))
			if !utils.Exists(path) {
				missingUIDs = append(missingUIDs, msg.Uid)
			}
		case <-fetchErr:
			DrainChannel(uidMsgs, 5*time.Second)
			break loop
		case <-ctx.Done():
			DrainChannel(uidMsgs, 5*time.Second)
			break loop
		}
	}

	for _, uid := range missingUIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)

		seq := new(imap.SeqSet)
		seq.AddNum(uid)
		section := &imap.BodySectionName{Peek: true}
		msgs := make(chan *imap.Message, 1)

		go func() { _ = c.UidFetch(seq, []imap.FetchItem{section.FetchItem()}, msgs) }()

		select {
		case msg := <-msgs:
			if msg == nil {
				break
			}
			body := msg.GetBody(section)
			if body == nil {
				break
			}
			data, err := io.ReadAll(body)
			if err == nil && !cfg.DryRun {
				path := MessagePath(cfg.BackupDir, box, uint64(uid))
				_ = os.WriteFile(path, data, 0644)
				mu.Lock()
				*downloaded++
				mu.Unlock()
			}
		case <-ctx.Done():
		}

		cancel()
		time.Sleep(50 * time.Millisecond)
	}
}

// ----------------------
// OAuth2 + Token Handling
// ----------------------

func authenticateOAuth2(c *client.Client, cfg config.Config) error {
	if cfg.OAuth2TokenFile == "" {
		return fmt.Errorf("OAUTH2_TOKEN_FILE is not set")
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(cfg.OAuth2TokenFile), 0700); err != nil {
		return fmt.Errorf("cannot create token dir: %w", err)
	}

	conf := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       []string{"https://mail.google.com/"},
		RedirectURL:  "http://localhost",
		Endpoint:     google.Endpoint,
	}

	ctx := context.Background()

	var token *oauth2.Token
	if t, err := loadTokenFromFile(cfg.OAuth2TokenFile); err == nil && t.Valid() {
		logrus.Infof("Loaded cached token from %s", cfg.OAuth2TokenFile)
		token = t
	}

	// First-time login
	if token == nil || !token.Valid() {
		authURL := conf.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
		fmt.Printf("Open this URL in a browser:\n%s\n\nCopy the code below:\nEnter code: ", authURL)

		var rawCode string
		fmt.Scanln(&rawCode)

		code, err := url.QueryUnescape(rawCode)
		if err != nil {
			return fmt.Errorf("invalid auth code: %w", err)
		}

		tok, err := conf.Exchange(ctx, code)
		if err != nil {
			return fmt.Errorf("OAuth2 code exchange failed: %w", err)
		}
		token = tok

		if err := saveTokenToFile(cfg.OAuth2TokenFile, token); err != nil {
			logrus.Warnf("Failed to save token: %v", err)
		} else {
			logrus.Infof("Saved token to %s", cfg.OAuth2TokenFile)
		}
	}

	// TokenSource auto-refresh
	ts := conf.TokenSource(ctx, token)

	getAccessToken := func() (string, error) {
		tok, err := ts.Token()
		if err != nil {
			return "", fmt.Errorf("failed to refresh token: %w", err)
		}
		if err := saveTokenToFile(cfg.OAuth2TokenFile, tok); err != nil {
			logrus.Warnf("Failed to save refreshed token: %v", err)
		}
		return tok.AccessToken, nil
	}

	// SASL XOAUTH2 client
	saslClient := &SASLOAuth2Client{
		Username: cfg.Email,
		TokenFn:  getAccessToken,
	}

	if err := c.Authenticate(saslClient); err != nil {
		return fmt.Errorf("XOAUTH2 IMAP authentication failed: %w", err)
	}

	logrus.Info("OAuth2 IMAP authentication successful")
	return nil
}

// SASLOAuth2Client implements go-sasl.Client for Gmail
type SASLOAuth2Client struct {
	Username string
	TokenFn  func() (string, error)
	stepDone bool
}

func (c *SASLOAuth2Client) Start() (mech string, ir []byte, err error) {
	token, err := c.TokenFn()
	if err != nil {
		return "", nil, err
	}
	payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.Username, token)
	return "XOAUTH2", []byte(payload), nil
}

func (c *SASLOAuth2Client) Next(challenge []byte) ([]byte, error) {
	if c.stepDone {
		return nil, io.EOF
	}
	c.stepDone = true
	return nil, nil
}

func (c *SASLOAuth2Client) Completed() bool {
	return c.stepDone
}

// Token file helpers
func loadTokenFromFile(filename string) (*oauth2.Token, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	return &token, json.Unmarshal(data, &token)
}

func saveTokenToFile(filename string, token *oauth2.Token) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0600)
}
