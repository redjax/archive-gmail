package main

import (
	"flag"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"

	config "github.com/redjax/archive-gmail/internal/config"
	gmailSvc "github.com/redjax/archive-gmail/internal/services/gmailService"
)

// runBackup executes the actual backup
func runBackup(cfg config.Config) {
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)

	useOAuth2 := cfg.ClientID != "" && cfg.ClientSecret != ""
	if cfg.Email == "" {
		logrus.Fatal("GMAIL_EMAIL is required")
	}
	if !useOAuth2 && cfg.Password == "" {
		logrus.Fatal("Either GMAIL_PASSWORD OR (GMAIL_CLIENT_ID + GMAIL_CLIENT_SECRET) required")
	}

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
	var mu sync.RWMutex

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

func main() {
	cfg := config.LoadConfig()

	flag.Parse()

	if cfg.CronSchedule == "" {
		// No schedule: run once and exit
		runBackup(cfg)
		return
	}

	// Only print schedule info if CronSchedule has a value
	logrus.Infof("Using schedule: %s", cfg.CronSchedule)

	var running int32

	// Parse the cron spec to calculate next run before starting
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(cfg.CronSchedule)
	if err != nil {
		logrus.Fatalf("Invalid cron schedule: %v", err)
	}

	// Print first scheduled run
	nextRun := sched.Next(time.Now())
	logrus.Infof("First scheduled backup at %s", nextRun.Format(time.RFC1123))

	// Create cron
	c := cron.New(cron.WithParser(parser))
	var id cron.EntryID
	id, err = c.AddFunc(cfg.CronSchedule, func() {
		if atomic.LoadInt32(&running) == 0 {
			atomic.StoreInt32(&running, 1)
			go func(localID cron.EntryID) {
				defer atomic.StoreInt32(&running, 0)
				logrus.Infof("Starting scheduled backup")
				runBackup(cfg)

				// Print next scheduled run
				next := c.Entry(localID).Next
				logrus.Infof("Next scheduled backup at %s", next.Format(time.RFC1123))
			}(id)
		} else {
			logrus.Info("Previous backup still running, skipping this tick")
		}
	})
	if err != nil {
		logrus.Fatalf("Failed to add cron function: %v", err)
	}

	c.Start()

	// Run first backup immediately
	if atomic.LoadInt32(&running) == 0 {
		atomic.StoreInt32(&running, 1)
		go func() {
			defer atomic.StoreInt32(&running, 0)
			logrus.Infof("Starting initial backup immediately")
			runBackup(cfg)

			// Print next scheduled run after first execution
			next := c.Entry(id).Next
			logrus.Infof("Next scheduled backup at %s", next.Format(time.RFC1123))
		}()
	}

	// Keep program running
	select {}
}
