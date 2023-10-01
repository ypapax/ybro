package ybro

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pkg/errors"
)

func SpentLimits(t1 time.Time, min, max time.Duration, msg string) string {
	dur := time.Since(t1)
	speedMsg := func() string {
		if dur <= min {
			return "fast"
		}
		if dur >= max {
			extraMax := 10 * max
			if dur >= extraMax {
				logrus.Warn("extra slow", errors.Errorf("this is too slow: %+v, extraMax: %+v", dur, extraMax))
				return "extra slow"
			}
			return "slow"
		}
		return "normal"
	}()
	return fmt.Sprintf("%s: time spent %s: %+v (%s-%s)", speedMsg, dur, msg, min, max)
}
