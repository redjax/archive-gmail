package gmailservice

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	config "github.com/redjax/archive-gmail/internal/config"
	"github.com/redjax/archive-gmail/internal/utils"
	"github.com/sirupsen/logrus"
)

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

func ListMailboxes(c *client.Client) ([]string, error) {
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

func MailboxDir(base, box string) string {
	safe := strings.ReplaceAll(box, "/", "_")

	return filepath.Join(base, safe)
}

func MessagePath(base, box string, msgID uint64) string {
	return filepath.Join(MailboxDir(base, box), fmt.Sprintf("%d.eml", msgID))
}

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

	c.Timeout = 5 * time.Minute // Reduced for faster failure detection

	if err := c.Login(cfg.Email, cfg.Password); err != nil {
		return nil, err
	}

	return c, nil
}

func ProcessMailbox(c *client.Client, box string, cfg config.Config, downloaded *uint64, mu *sync.RWMutex) {
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

	if err := utils.EnsureDir(MailboxDir(cfg.BackupDir, box), cfg.DryRun); err != nil {
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
			path := MessagePath(cfg.BackupDir, box, uint64(msg.Uid))
			if !utils.Exists(path) {
				missingUIDs = append(missingUIDs, msg.Uid)
				missingCount++
			}
		case err := <-fetchErr:
			if err != nil {
				logrus.Warnf("  [%s] UID FETCH failed: %v", box, err)
			}
			DrainChannel(uidMsgs, 5*time.Second)
			break loop
		case <-ctx.Done():
			logrus.Warnf("  [%s] UID FETCH timeout after %d msgs", box, msgCount)
			DrainChannel(uidMsgs, 5*time.Second)
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
				DrainChannel(msgs, 1*time.Second)
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

		path := MessagePath(cfg.BackupDir, box, uint64(uid))
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
