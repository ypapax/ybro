package ybro

import (
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func Do(iteration func() error, retryDelay, timeout time.Duration) error {
	done := make(chan bool)
	var lastError error
	lf := logrus.WithField("retryDelay", retryDelay).WithField("timeout", timeout)
	giveUp := make(chan bool)
	attempt := 0
	go func() {
		for {
			li := lf.WithField("attempt", attempt)
			select {
			case <-giveUp:
				li.Tracef("give up")
				return
			default:
			}
			attempt++
			if err := iteration(); err != nil {
				lastError = errors.Wrapf(err, "attempt: %+v", attempt)
				li.Tracef("%+v, will retry after %s", lastError, retryDelay)
				time.Sleep(retryDelay)
				continue
			}
			li.Tracef("good")
			done <- true
			break
		}
	}()
	select {
	case <-time.After(timeout):
		go func() {
			giveUp <- true
		}()
		return errors.Errorf("attempt: %+v, timeout %s, lastError: %+v", attempt, timeout, lastError)
	case <-done:
		return nil
	}
}
