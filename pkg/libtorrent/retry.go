package libtorrent

import (
	"log"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

const (
	// retries of a failed chunk write before the download is disabled for good
	writeRetries = 6
	// the first delay, doubled on each retry: 0.5s, 1s, 2s, 4s, 8s, 16s
	writeRetryDelay = 500 * time.Millisecond
	// the retries start over when no write failed for this long
	writeRetryReset = time.Minute
)

// downloadToggler is the part of a torrent the retries act on
type downloadToggler interface {
	AllowDataDownload()
	DisallowDataDownload()
}

// writeRetrier handles the errors writing downloaded chunks to disk. The default
// handler disables the download of the torrent for good, but on Windows another
// process, e.g. an antivirus, can briefly hold a file open while it is written.
// The failed chunk is requested again, so the download is paused with an
// exponential backoff and resumed instead.
type writeRetrier struct {
	t     downloadToggler
	name  string
	sleep func(time.Duration)
	now   func() time.Time

	mutex       sync.Mutex
	retrying    bool
	failures    int
	lastFailure time.Time
}

// retryWriteErrors installs a writeRetrier on t
func retryWriteErrors(t *torrent.Torrent) {
	r := &writeRetrier{t: t, name: t.InfoHash().HexString(), sleep: time.Sleep, now: time.Now}
	t.SetOnWriteChunkError(r.onError)
}

// onError is called in its own goroutine for each failed chunk write
func (r *writeRetrier) onError(err error) {
	r.mutex.Lock()
	// writes from several peers fail together, one pause covers all of them
	if r.retrying {
		r.mutex.Unlock()
		return
	}
	if r.now().Sub(r.lastFailure) > writeRetryReset {
		r.failures = 0
	}
	r.failures++
	r.lastFailure = r.now()
	failures := r.failures
	if failures > writeRetries {
		r.mutex.Unlock()
		log.Printf("torrent %s: writing to disk failed %d times, disabling the download: %v", r.name, writeRetries, err)
		r.t.DisallowDataDownload()
		return
	}
	r.retrying = true
	r.mutex.Unlock()

	delay := writeRetryDelay << (failures - 1)
	log.Printf("torrent %s: writing to disk failed, retrying in %v (%d/%d): %v", r.name, delay, failures, writeRetries, err)
	r.t.DisallowDataDownload()
	r.sleep(delay)
	r.t.AllowDataDownload()

	r.mutex.Lock()
	r.retrying = false
	r.lastFailure = r.now()
	r.mutex.Unlock()
}
