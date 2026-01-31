package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/sirupsen/logrus"
)

type Config struct {
	Email         string
	Password      string
	BackupDir     string
	ImapServer    string
	ImapPort      int
	FoldersOnly   map[string]bool
	MaxWorkers    int
	DryRun        bool
	TLSSkipVerify bool
	LogLevel      string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		return strings.ToLower(v) == "true"
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func loadConfig() Config {
	folders := map[string]bool{}
	if v := os.Getenv("FOLDERS_ONLY"); v != "" {
		for _, f := range strings.Split(v, ",") {
			folders[strings.TrimSpace(f)] = true
		}
	}

	return Config{
		Email:         os.Getenv("GMAIL_EMAIL"),
		Password:      os.Getenv("GMAIL_PASSWORD"),
		BackupDir:     getenv("BACKUP_DIR", "./backups"),
		ImapServer:    getenv("IMAP_SERVER", "imap.gmail.com"),
		ImapPort:      getenvInt("IMAP_PORT", 993),
		FoldersOnly:   folders,
		MaxWorkers:    getenvInt("MAX_WORKERS", 1), // Single worker
		DryRun:        getenvBool("DRY_RUN", false),
		TLSSkipVerify: getenvBool("TLS_SKIP_VERIFY", false),
		LogLevel:      getenv("LOG_LEVEL", "INFO"),
	}
}

func connect(cfg Config) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.ImapServer, cfg.ImapPort)

	tlsCfg := &tls.Config{
		ServerName:         cfg.ImapServer,
		InsecureSkipVerify: cfg.TLSSkipVerify,
	}

	c, err := client.DialTLS(addr, tlsCfg)
	if err != nil {
		return nil, err
	}

	c.Timeout = 5 * time.Minute // Reduced for faster failure detection

	if err := c.Login(cfg.Email, cfg.Password); err != nil {
		return nil, err
	}

	return c, nil
}

func drainChannel[T any](ch <-chan T, maxWait time.Duration) {
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

func listMailboxes(c *client.Client) ([]string, error) {
	ch := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)

	go func() {
		done <- c.List("", "*", ch)
	}()

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

func mailboxDir(base, box string) string {
	safe := strings.ReplaceAll(box, "/", "_")
	return filepath.Join(base, safe)
}

func ensureDir(path string, dry bool) error {
	if dry {
		return nil
	}
	return os.MkdirAll(path, 0755)
}

func messagePath(base, box string, msgID uint64) string {
	return filepath.Join(mailboxDir(base, box), fmt.Sprintf("%d.eml", msgID))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func processMailbox(c *client.Client, box string, cfg Config, downloaded *uint64, mu *sync.RWMutex) {
	logrus.Infof("Processing: %s", box)

	// Robust SELECT with retries
	var mboxStatus *imap.MailboxStatus
	var selectErr error
	for retry := 0; retry < 3; retry++ {
		mboxStatus, selectErr = c.Select(box, true)
		if selectErr == nil {
			break
		}
		logrus.Warnf("SELECT %s failed (attempt %d/3): %v", box, retry+1, selectErr)
		if retry < 2 {
			time.Sleep(time.Duration(retry+1) * time.Second)
		}
	}

	if selectErr != nil || mboxStatus == nil || mboxStatus.Messages == 0 {
		logrus.Infof("  Skipping %s: empty or SELECT failed", box)
		return
	}

	logrus.Infof("%s: %d total messages (UID range 1-%d)", box, mboxStatus.Messages, mboxStatus.UidNext-1)

	if err := ensureDir(mailboxDir(cfg.BackupDir, box), cfg.DryRun); err != nil {
		logrus.Warnf("Failed to create dir for %s: %v", box, err)
		return
	}

	// Get all existing UIDs in single command
	logrus.Infof("%s: Fetching all %d UIDs (one-shot)", box, mboxStatus.Messages)

	uidSeq := new(imap.SeqSet)
	uidSeq.AddRange(1, mboxStatus.UidNext-1)
	uidMsgs := make(chan *imap.Message, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	fetchErr := make(chan error, 1)

	go func() {
		fetchErr <- c.UidFetch(uidSeq, []imap.FetchItem{imap.FetchUid}, uidMsgs)
	}()

	missingUIDs := make([]uint32, 0, mboxStatus.Messages/10)
	msgCount := 0
	missingCount := 0

loop:
	for {
		select {
		case msg, ok := <-uidMsgs:
			if !ok {
				break loop
			}
			msgCount++
			path := messagePath(cfg.BackupDir, box, uint64(msg.Uid))
			if !exists(path) {
				missingUIDs = append(missingUIDs, msg.Uid)
				missingCount++
			}
		case err := <-fetchErr:
			if err != nil {
				logrus.Warnf("  [%s] UID FETCH failed: %v", box, err)
			}
			drainChannel(uidMsgs, 5*time.Second)
			break loop
		case <-ctx.Done():
			logrus.Warnf("  [%s] UID FETCH timeout after %d msgs", box, msgCount)
			drainChannel(uidMsgs, 5*time.Second)
			break loop
		}
	}

	cancel()
	logrus.Infof("%s: Scanned %d/%d UIDs, found %d missing", box, msgCount, mboxStatus.Messages, len(missingUIDs))

	if len(missingUIDs) == 0 {
		logrus.Infof("%s: Nothing new to download (%d total)", box, mboxStatus.Messages)
		return
	}

	logrus.Infof("%s: Downloading %d missing messages", box, len(missingUIDs))

	// Download missing messages
	savedCount := 0
	for i, uid := range missingUIDs {
		if i%20 == 0 && i > 0 {
			pct := 100 * float64(i) / float64(len(missingUIDs))
			logrus.Infof("  [%s] Downloading %d/%d (%.0f%%)", box, i, len(missingUIDs), pct)
		}

		// Single message fetch with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		seq := new(imap.SeqSet)
		seq.AddNum(uid)
		section := &imap.BodySectionName{Peek: true}
		items := []imap.FetchItem{section.FetchItem()}
		msgs := make(chan *imap.Message, 1)

		fetchErr := make(chan error, 1)
		go func() {
			fetchErr <- c.UidFetch(seq, items, msgs)
		}()

		select {
		case err := <-fetchErr:
			if err != nil {
				logrus.Debugf("[%s] Failed UID %d: %v", box, uid, err)
				drainChannel(msgs, 1*time.Second)
				continue
			}
		case <-ctx.Done():
			logrus.Debugf("[%s] Timeout UID %d", box, uid)
			continue
		}

		msg := <-msgs
		if msg == nil {
			continue
		}

		body := msg.GetBody(section)
		if body == nil {
			continue
		}

		data, err := io.ReadAll(body)
		if err != nil {
			logrus.Debugf("[%s] Read failed UID %d: %v", box, uid, err)
			continue
		}

		path := messagePath(cfg.BackupDir, box, uint64(uid))
		if cfg.DryRun {
			continue
		}

		if err := os.WriteFile(path, data, 0644); err != nil {
			logrus.Debugf("[%s] Write failed %s: %v", box, path, err)
			continue
		}

		savedCount++
		mu.Lock()
		*downloaded++
		mu.Unlock()

		time.Sleep(50 * time.Millisecond) // Gmail politeness
	}

	logrus.Infof("%s COMPLETE: %d/%d saved", box, savedCount, len(missingUIDs))
}

func main() {
	cfg := loadConfig()

	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)

	if cfg.Email == "" || cfg.Password == "" {
		logrus.Fatal("GMAIL_EMAIL and GMAIL_PASSWORD are required")
	}

	c, err := connect(cfg)
	if err != nil {
		logrus.Fatalf("IMAP connect failed: %v", err)
	}
	defer c.Logout()

	mailboxes, err := listMailboxes(c)
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
			processMailbox(c, boxName, cfg, &downloaded, &mu)
		}(box)
	}

	wg.Wait()

	elapsed := time.Since(start).Seconds()
	rate := float64(downloaded) / elapsed
	logrus.Infof("Archive complete: %d messages in %.1fs (%.2f msg/sec)", downloaded, elapsed, rate)
}
