package main

import (
	"fmt"
	"io"
	"log"
	"log/syslog"
	"os"
	"strings"
	"time"

	"github.com/fsouza/go-dockerclient"
	"github.com/hashicorp/logutils"
	"github.com/jessevdk/go-flags"

	"github.com/umputun/docker-logger/app/discovery"
	"github.com/umputun/docker-logger/app/logger"
	"github.com/umputun/docker-logger/app/logger/rotator"
)

var opts struct {
	DockerHost    string        `short:"d" long:"docker" env:"DOCKER_HOST" default:"unix:///var/run/docker.sock" description:"docker host"`
	SyslogHost    string        `long:"syslog-host" env:"SYSLOG_HOST" default:"127.0.0.1:514" description:"syslog host"`
	EnableFiles   bool          `long:"files" env:"LOG_FILES" description:"enable logging to files"`
	EnableSyslog  bool          `long:"syslog" env:"LOG_SYSLOG" description:"enable logging to syslog"`
	MaxFileSize   int64         `long:"max-size" env:"MAX_SIZE" default:"10" description:"size of log triggering rotation (MB)"`
	MaxFilesCount int           `long:"max-files" env:"MAX_FILES" default:"5" description:"number of rotated files to keep"`
	Excludes      []string      `short:"x" long:"exclude" env:"EXCLUDE" env-delim:"," description:"excluded container names"`
	FlushRecs     int           `long:"flush-recs" env:"FLUSH_RECS" default:"100" description:"flush every N records"`
	FlushInterval time.Duration `long:"flush-time" env:"FLUSH_TIME" default:"1s" description:"flush inactivity time"`
	Dbg           bool          `long:"dbg" env:"DEBUG" description:"debug mode"`
}

var revision = "unknown"

func main() {
	log.Printf("docker-logger %s", revision)
	if _, err := flags.Parse(&opts); err != nil {
		os.Exit(1)
	}
	setupLog(opts.Dbg)

	log.Printf("[INFO] options: %+v", opts)

	client, err := docker.NewClient(opts.DockerHost)
	if err != nil {
		log.Fatalf("[ERROR] failed to make docker client %s, %v", opts.DockerHost, err)
	}

	events, err := discovery.NewEventNotif(client, opts.Excludes)
	if err != nil {
		log.Fatalf("[ERROR] failed to make event notifier, %v", err)
	}

	containerLogs := map[string]logger.LogStreamer{}

	for event := range events.Channel() {

		if event.Status {

			logWriter, errWriter := MakeLogWriters(event.ContainerName, event.Group)
			ls := logger.LogStreamer{
				DockerClient:  client,
				ContainerID:   event.ContainerID,
				ContainerName: event.ContainerName,
				LogWriter:     logWriter,
				ErrWriter:     errWriter,
			}
			containerLogs[event.ContainerID] = ls
			ls.Go()
		} else {

			if ls, ok := containerLogs[event.ContainerID]; ok {
				log.Printf("[DEBUG] close loggers for %+v", event)
				if e := ls.LogWriter.Close(); e != nil {
					log.Printf("[WARN] failed to close log writer for %+v, %s", event, e)
				}
				if e := ls.ErrWriter.Close(); e != nil {
					log.Printf("[WARN] failed to close err writer for %+v, %s", event, e)
				}
				delete(containerLogs, event.ContainerID)
			}
		}
	}
}

// MakeLogWriters creates io.Writer with rotated out and separate err files. Also adds writer for remote syslog
func MakeLogWriters(containerName string, group string) (logWriter, errWriter io.WriteCloser) {
	log.Printf("[DEBUG] create log writer for %s/%s", group, containerName)
	if !opts.EnableFiles && !opts.EnableSyslog {
		log.Printf("[ERROR] either files or syslog has to be enabled")
	}

	logWriters := []io.WriteCloser{} // collect log writers here, for MultiWriter use
	errWriters := []io.WriteCloser{} // collect err writers here, for MultiWriter use

	if opts.EnableFiles {

		logDir := "logs"
		if group != "" {
			logDir = fmt.Sprintf("logs/%s", group)
		}
		if err := os.MkdirAll(logDir, 0700); err != nil {
			log.Fatalf("[ERROR] can't make directory %s, %v", logDir, err)
		}

		logName := fmt.Sprintf("%s/%s.log", logDir, containerName)

		maxSize := opts.MaxFileSize * 1024 * 1024
		logFileWriter, err := rotator.New(logName, rotator.MaxSize(maxSize), rotator.MaxFiles(opts.MaxFilesCount),
			rotator.Buffer(opts.FlushRecs), rotator.Interval(opts.FlushInterval))
		if err != nil {
			log.Printf("[WARN] can't make logger for %s", logName)
		}

		errFname := strings.Replace(logName, ".log", ".err", 1)

		errFileWriter, err := rotator.New(errFname, rotator.MaxSize(maxSize), rotator.MaxFiles(opts.MaxFilesCount),
			rotator.Buffer(opts.FlushRecs), rotator.Interval(opts.FlushInterval))
		if err != nil {
			log.Printf("[WARN] can't make logger for %s", errFname)
		}

		logWriters = append(logWriters, logFileWriter)
		errWriters = append(errWriters, errFileWriter)
		log.Printf("[INFO] loggers created for %s and %s, max.size=%d, max.files=%d, flush.recs=%d, flush.interval=%v",
			logName, errFname, maxSize, opts.MaxFilesCount, opts.FlushRecs, opts.FlushInterval)
	}

	if opts.EnableSyslog {
		syslogWriter, err := syslog.Dial("udp4", opts.SyslogHost, syslog.LOG_WARNING|syslog.LOG_DAEMON, "docker/"+containerName)

		if err == nil {
			logWriters = append(logWriters, syslogWriter)
			errWriters = append(errWriters, syslogWriter)
		} else {
			log.Printf("[WARN] can't connect to syslog, %v", err)
		}
	}

	return logger.NewMultiWriterIgnoreErrors(logWriters...), logger.NewMultiWriterIgnoreErrors(errWriters...)
}

func setupLog(dbg bool) {
	filter := &logutils.LevelFilter{
		Levels:   []logutils.LogLevel{"DEBUG", "INFO", "WARN", "ERROR"},
		MinLevel: logutils.LogLevel("INFO"),
		Writer:   os.Stdout,
	}

	log.SetFlags(log.Ldate | log.Ltime)

	if dbg {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
		filter.MinLevel = logutils.LogLevel("DEBUG")
	}
	log.SetOutput(filter)
}