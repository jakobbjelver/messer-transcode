package node

import (
	"context"
	"sync"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/transcode"
)

// egress runs the place step off the encode path.
//
// Placing a finished file is a copy across volumes plus two renames: about
// thirty seconds for a feature, which is 12 % of an encode spent with the media
// engine idle. Running it here lets the next file start immediately; the
// ordering guarantee ("the original is only touched by the final rename") is
// unchanged, because that lives inside Commit.
type egress struct {
	ch   chan egressItem
	wg   sync.WaitGroup
	once sync.Once
	log  func(transcode.Event)
}

type egressItem struct {
	id      int64
	path    string
	run     func(context.Context) error
	done    func(time.Duration)
	started time.Time
}

func newEgress(log func(transcode.Event)) *egress {
	e := &egress{ch: make(chan egressItem, 4), log: log}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for item := range e.ch {
			started := time.Now()
			err := item.run(context.Background())
			if err != nil {
				e.log(transcode.Event{FileID: item.id, Path: item.path, Stage: "egress",
					Message: "failed: " + err.Error()})
			} else {
				e.log(transcode.Event{FileID: item.id, Path: item.path, Stage: "egress",
					Message: "placed in " + time.Since(started).Round(time.Second).String()})
			}
			if item.done != nil {
				item.done(time.Since(started))
			}
		}
	}()
	return e
}

// submit queues a commit. The caller must not run the commit itself.
func (e *egress) submit(item egressItem) {
	e.ch <- item
}

// drain stops accepting work and waits for what is queued. The bound matters:
// a hung copy must not hold shutdown forever, and PM2 will kill us anyway.
func (e *egress) drain(limit time.Duration) bool {
	e.once.Do(func() { close(e.ch) })

	finished := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return true
	case <-time.After(limit):
		return false
	}
}
