package app

import (
	"time"

	genericfilters "k8s.io/apiserver/pkg/server/filters"
)

const watchDrainPollInterval = 100 * time.Millisecond

type watchDrainSignal struct {
	shuttingDown chan struct{}
}

func newWatchDrainSignal(shutdownInitiated <-chan struct{}, watches genericfilters.RequestWaitGroup, poll time.Duration) *watchDrainSignal {
	s := &watchDrainSignal{shuttingDown: make(chan struct{})}
	go s.closeWhenDrainStarts(shutdownInitiated, watches, poll)
	return s
}

func (s *watchDrainSignal) ShuttingDown() <-chan struct{} {
	return s.shuttingDown
}

func (s *watchDrainSignal) closeWhenDrainStarts(shutdownInitiated <-chan struct{}, watches genericfilters.RequestWaitGroup, poll time.Duration) {
	<-shutdownInitiated
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if err := watches.Add(1); err != nil {
			close(s.shuttingDown)
			return
		}
		watches.Done()
		<-ticker.C
	}
}
