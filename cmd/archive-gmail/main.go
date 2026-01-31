package main

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	config "github.com/redjax/archive-gmail/internal/config"
	gmailSvc "github.com/redjax/archive-gmail/internal/services/gmailService"
)

func main() {
	cfg := config.LoadConfig()

	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)

	// SINGLE validation block - OAuth2 OR Password
	useOAuth2 := cfg.ClientID != "" && cfg.ClientSecret != ""
	if cfg.Email == "" {
		logrus.Fatal("GMAIL_EMAIL is required")
	}
	if !useOAuth2 && cfg.Password == "" {
		logrus.Fatal("Either GMAIL_PASSWORD OR (GMAIL_CLIENT_ID + GMAIL_CLIENT_SECRET) required")
	}

	// Connect handles auth logic
	c, err := gmailSvc.Connect(cfg)
	if err != nil {
		logrus.Fatalf("IMAP connect failed: %v", err)
	}
	defer c.Logout()

	mailboxes, err := gmailSvc.ListMailboxes(c)
	if err != nil {
		logrus.Fatalf("Failed listing mailboxes: %v", err)
	}

	start := time.Now()
	var downloaded uint64
	mu := sync.RWMutex{}

	sem := make(chan struct{}, cfg.MaxWorkers)
	var wg sync.WaitGroup

	logrus.Infof("Starting backup with %d workers across %d mailboxes", cfg.MaxWorkers, len(mailboxes))

	for _, box := range mailboxes {
		if len(cfg.FoldersOnly) > 0 && !cfg.FoldersOnly[box] {
			continue
		}

		sem <- struct{}{}
		wg.Add(1)

		go func(boxName string) {
			defer wg.Done()
			defer func() { <-sem }()
			gmailSvc.ProcessMailbox(c, boxName, cfg, &downloaded, &mu)
		}(box)
	}

	wg.Wait()

	elapsed := time.Since(start).Seconds()
	rate := float64(downloaded) / elapsed
	logrus.Infof("Archive complete: %d messages in %.1fs (%.2f msg/sec)", downloaded, elapsed, rate)
}
