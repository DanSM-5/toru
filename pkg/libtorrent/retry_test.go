package libtorrent

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeTorrent records the calls of a writeRetrier
type fakeTorrent struct {
	mutex  sync.Mutex
	events []string
}

func (f *fakeTorrent) AllowDataDownload()    { f.record("allow") }
func (f *fakeTorrent) DisallowDataDownload() { f.record("disallow") }

func (f *fakeTorrent) record(event string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.events = append(f.events, event)
}

// newTestRetrier returns a retrier whose sleeps only advance a fake clock
func newTestRetrier() (*writeRetrier, *fakeTorrent, *[]time.Duration, *time.Time) {
	t := &fakeTorrent{}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	delays := []time.Duration{}
	r := &writeRetrier{
		t:     t,
		name:  "test",
		sleep: func(d time.Duration) { delays = append(delays, d); clock = clock.Add(d) },
		now:   func() time.Time { return clock },
	}
	return r, t, &delays, &clock
}

var errLocked = errors.New("The process cannot access the file because it is being used by another process.")

func TestWriteRetryBackoff(t *testing.T) {
	r, torrent, delays, _ := newTestRetrier()
	for i := 0; i < writeRetries; i++ {
		r.onError(errLocked)
	}

	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if !reflect.DeepEqual(*delays, want) {
		t.Errorf("delays: %v, want %v", *delays, want)
	}
	// paused then resumed on every retry
	for i, event := range torrent.events {
		if want := []string{"disallow", "allow"}[i%2]; event != want {
			t.Fatalf("event %d: %s, want %s (%v)", i, event, want, torrent.events)
		}
	}

	// gives up after the last retry, leaving the download disabled
	r.onError(errLocked)
	if len(*delays) != writeRetries || torrent.events[len(torrent.events)-1] != "disallow" {
		t.Errorf("should give up: delays %v, events %v", *delays, torrent.events)
	}
}

func TestWriteRetryReset(t *testing.T) {
	r, _, delays, clock := newTestRetrier()
	r.onError(errLocked)
	r.onError(errLocked)

	// no failure for a while: the retries start over
	*clock = clock.Add(writeRetryReset + time.Second)
	r.onError(errLocked)

	want := []time.Duration{500 * time.Millisecond, time.Second, 500 * time.Millisecond}
	if !reflect.DeepEqual(*delays, want) {
		t.Errorf("delays: %v, want %v", *delays, want)
	}
}

func TestWriteRetryConcurrentErrors(t *testing.T) {
	r, torrent, _, _ := newTestRetrier()
	sleeping := make(chan bool)
	release := make(chan bool)
	r.sleep = func(time.Duration) {
		sleeping <- true
		<-release
	}

	done := make(chan bool)
	go func() {
		r.onError(errLocked)
		done <- true
	}()
	<-sleeping

	// failures of other chunks during the pause are covered by it
	r.onError(errLocked)
	r.onError(errLocked)
	close(release)
	<-done

	if !reflect.DeepEqual(torrent.events, []string{"disallow", "allow"}) || r.failures != 1 {
		t.Errorf("events %v, failures %d", torrent.events, r.failures)
	}
}
