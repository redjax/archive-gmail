package main

import (
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

/* ================= CONFIG ================= */

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

/* ================= IMAP ================= */

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

	c.Timeout = 5 * time.Minute

	if err := c.Login(cfg.Email, cfg.Password); err != nil {
		return nil, err
	}

	return c, nil
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

/* ================= STORAGE ================= */

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

/* ================= WORKER ================= */

func processMailbox(c *client.Client, box string, cfg Config, downloaded *uint64, mu *sync.RWMutex) {
	logrus.Infof("Processing: %s", box)

	// Sequential SELECT with retry
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
		logrus.Infof("Skipping %s: empty or SELECT failed", box)
		return
	}

	// Skip huge mailboxes
	if mboxStatus.Messages > 50000 {
		logrus.Warnf("Skipping huge mailbox %s (%d msgs)", box, mboxStatus.Messages)
		return
	}

	if err := ensureDir(mailboxDir(cfg.BackupDir, box), cfg.DryRun); err != nil {
		logrus.Warnf("Failed to create dir for %s: %v", box, err)
		return
	}

	// Phase 1: Get missing UIDs
	logrus.Debugf("Scanning UIDs for %s (%d total)", box, mboxStatus.Messages)
	uidSeq := new(imap.SeqSet)
	uidSeq.AddRange(1, mboxStatus.UidNext-1)
	uidMsgs := make(chan *imap.Message, 1000)

	if err := c.UidFetch(uidSeq, []imap.FetchItem{imap.FetchUid}, uidMsgs); err != nil {
		logrus.Warnf("UID scan failed for %s: %v", box, err)
		return
	}

	missingUIDs := make([]uint32, 0, mboxStatus.Messages)
	for msg := range uidMsgs {
		path := messagePath(cfg.BackupDir, box, uint64(msg.Uid))
		if !exists(path) {
			missingUIDs = append(missingUIDs, msg.Uid)
		}
	}

	if len(missingUIDs) == 0 {
		logrus.Infof("Mailbox %s: Nothing to download (%d total)", box, mboxStatus.Messages)
		return
	}

	logrus.Infof("Mailbox %s: %d missing messages", box, len(missingUIDs))

	// Phase 2: Fetch SINGLE messages only
	for _, uid := range missingUIDs {
		seq := new(imap.SeqSet)
		seq.AddNum(uid)

		section := &imap.BodySectionName{Peek: true}
		items := []imap.FetchItem{section.FetchItem()}
		msgs := make(chan *imap.Message, 1)

		if err := c.UidFetch(seq, items, msgs); err != nil {
			logrus.Warnf("Fetch failed UID %d in %s: %v", uid, box, err)
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
			logrus.Warnf("Read failed UID %d in %s: %v", uid, box, err)
			continue
		}

		path := messagePath(cfg.BackupDir, box, uint64(uid))
		if cfg.DryRun {
			logrus.Debugf("[DRY-RUN] Would write %s (%d bytes)", path, len(data))
			continue
		}

		if writeErr := os.WriteFile(path, data, 0644); writeErr != nil {
			logrus.Warnf("Write failed %s: %v", path, writeErr)
			continue
		}

		mu.Lock()
		*downloaded++
		mu.Unlock()
		logrus.Debugf("Saved UID %d -> %s (%d bytes)", uid, path, len(data))
	}
}

/* ================= MAIN ================= */

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

	// SEQUENTIAL PROCESSING - NO GOROUTINES
	for _, box := range mailboxes {
		if len(cfg.FoldersOnly) > 0 && !cfg.FoldersOnly[box] {
			continue
		}

		processMailbox(c, box, cfg, &downloaded, &mu)

		// Rate limiting between mailboxes
		time.Sleep(500 * time.Millisecond)
	}

	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		rate := float64(downloaded) / elapsed
		logrus.Infof("Archive complete: %d messages in %.1fs (%.2f msg/sec)",
			downloaded, elapsed, rate)
	} else {
		logrus.Info("Archive complete")
	}
}
