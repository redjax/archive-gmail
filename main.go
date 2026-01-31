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

	logrus.Infof("Scanning UIDs for %s (%d messages)", box, mboxStatus.Messages)

	missingUIDs := make([]uint32, 0, mboxStatus.Messages/10)
	chunkSize := uint32(2000)
	totalChunks := (mboxStatus.UidNext + chunkSize - 1) / chunkSize

	scanStart := time.Now()
	for chunkStart := uint32(1); chunkStart < mboxStatus.UidNext; chunkStart += chunkSize {
		chunkEnd := chunkStart + chunkSize - 1
		if chunkEnd > mboxStatus.UidNext {
			chunkEnd = mboxStatus.UidNext
		}

		shortBox := box
		if len(box) > 20 {
			shortBox = box[:20]
		}
		chunkNum := (chunkStart-1)/chunkSize + 1
		pct := int(100 * float64(chunkStart) / float64(mboxStatus.UidNext))
		logrus.Infof("[%s] Chunk %d/%d (UIDs %d-%d) %d%%",
			shortBox, chunkNum, totalChunks, chunkStart, chunkEnd, pct)

		uidSeq := new(imap.SeqSet)
		uidSeq.AddRange(chunkStart, chunkEnd)
		uidMsgs := make(chan *imap.Message, 200)

		if err := c.UidFetch(uidSeq, []imap.FetchItem{imap.FetchUid}, uidMsgs); err != nil {
			logrus.Warnf("└─ Chunk %d-%d failed: %v", chunkStart, chunkEnd, err)
			continue
		}

		chunkMissing := 0
		for msg := range uidMsgs {
			path := messagePath(cfg.BackupDir, box, uint64(msg.Uid))
			if !exists(path) {
				missingUIDs = append(missingUIDs, msg.Uid)
				chunkMissing++
			}
		}

		if chunkNum%5 == 0 || chunkNum == totalChunks {
			scanElapsed := time.Since(scanStart).Seconds()
			remainingChunks := totalChunks - chunkNum
			eta := scanElapsed * float64(remainingChunks) / float64(chunkNum)
			logrus.Infof("Scan progress: %d/%d chunks, %d missing so far (ETA: %.0fs)",
				chunkNum, totalChunks, len(missingUIDs), eta)
		}
	}

	scanElapsed := time.Since(scanStart).Seconds()
	pctMissing := 100 * float64(len(missingUIDs)) / float64(mboxStatus.Messages)
	logrus.Infof("Scan complete: %.1fs, found %d missing / %.1f%% of %d total",
		scanElapsed, len(missingUIDs), pctMissing, mboxStatus.Messages)

	if len(missingUIDs) == 0 {
		logrus.Infof("%s: Nothing new to download", box)
		return
	}

	// Download with progress
	logrus.Infof("  Downloading %d messages from %s", len(missingUIDs), box)

	downloadStart := time.Now()
	savedCount := 0

	for i, uid := range missingUIDs {
		// Progress every 100 messages
		if i > 0 && i%100 == 0 {
			pct := 100 * float64(i) / float64(len(missingUIDs))
			elapsed := time.Since(downloadStart).Seconds()
			rate := float64(i) / elapsed
			eta := elapsed * float64(len(missingUIDs)-i) / float64(i)
			shortBox := box
			if len(box) > 20 {
				shortBox = box[:20]
			}
			logrus.Infof("⬇️  [%s] %d/%d (%.1f%%, %.0f msg/s, ETA: %.0fs)",
				shortBox, i, len(missingUIDs), pct, rate, eta)
		}

		seq := new(imap.SeqSet)
		seq.AddNum(uid)
		section := &imap.BodySectionName{Peek: true}
		items := []imap.FetchItem{section.FetchItem()}
		msgs := make(chan *imap.Message, 1)

		if err := c.UidFetch(seq, items, msgs); err != nil {
			logrus.Debugf("Fetch failed UID %d: %v", uid, err)
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
			logrus.Debugf("Read failed UID %d: %v", uid, err)
			continue
		}

		path := messagePath(cfg.BackupDir, box, uint64(uid))
		if cfg.DryRun {
			continue
		}

		if err := os.WriteFile(path, data, 0644); err != nil {
			logrus.Debugf("Write failed %s: %v", path, err)
			continue
		}

		savedCount++
		mu.Lock()
		*downloaded++
		mu.Unlock()
	}

	downloadElapsed := time.Since(downloadStart).Seconds()
	logrus.Infof("%s COMPLETE: %d/%d saved (%.1f msg/s)",
		box, savedCount, len(missingUIDs), float64(savedCount)/downloadElapsed)
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

	// Parallel processing using workers
	sem := make(chan struct{}, cfg.MaxWorkers)
	var wg sync.WaitGroup

	logrus.Infof("Starting backup with %d workers across %d mailboxes", cfg.MaxWorkers, len(mailboxes))

	for _, box := range mailboxes {
		if len(cfg.FoldersOnly) > 0 && !cfg.FoldersOnly[box] {
			continue
		}

		// Wait for available worker slot
		sem <- struct{}{}
		wg.Add(1)

		go func(boxName string) {
			defer wg.Done()
			defer func() { <-sem }() // Release worker slot

			processMailbox(c, boxName, cfg, &downloaded, &mu)
		}(box)
	}

	wg.Wait()

	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		rate := float64(downloaded) / elapsed
		logrus.Infof("Archive complete: %d messages in %.1fs (%.2f msg/sec)",
			downloaded, elapsed, rate)
	} else {
		logrus.Info("Archive complete")
	}
}
