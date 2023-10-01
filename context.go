package ybro

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"runtime/debug"
	"strings"
	"time"
)

type stackTimeout struct {
	Stack      string
	Timeout    time.Duration
	Expiration time.Time
}

type stackTimeouts struct {
	Vals []stackTimeout
}

func (s stackTimeouts) String() string {
	if s.Vals == nil && len(s.Vals) == 0 {
		return "-"
	}
	var parst []string
	for _, si := range s.Vals {
		// elem := fmt.Sprintf("ouuut%+v: %+v", si.Timeout, spew.InlineSep(si.Stack, " lll "))
		fileName := SaveFileDebugExt(si.Stack, "txt")
		elem := fmt.Sprintf("ouuut%+v: %+v, expiration: %+v, expires in %+v, expired: %+v",
			si.Timeout, fileName, si.Expiration, si.Expiration.Sub(time.Now()), si.Expiration.Before(time.Now()))
		parst = append(parst, elem)
	}
	return strings.Join(parst, "))))")
}

func GenerateTimeoutContextWithCancel(ctxInput context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	const contextTimeoutStackKey = "timeout-key"
	expiration := time.Now().Add(timeout)
	stacks := func() stackTimeouts {
		existingStack := ctxInput.Value(contextTimeoutStackKey)
		if existingStack == nil {
			return stackTimeouts{Vals: []stackTimeout{{Stack: string(debug.Stack()), Timeout: timeout, Expiration: expiration}}}
		}
		existingStackConverted, ok := existingStack.(stackTimeouts)
		if !ok {
			logrus.Warnf("couldn't convert type")
			return stackTimeouts{Vals: []stackTimeout{{Stack: string(debug.Stack()), Timeout: timeout, Expiration: expiration}}}
		}
		existingStackConverted.Vals = append(existingStackConverted.Vals, stackTimeout{Stack: string(debug.Stack()), Timeout: timeout, Expiration: expiration})
		return existingStackConverted
	}()
	ctxV := context.WithValue(ctxInput, contextTimeoutStackKey, stacks)
	if timeout == 0 {
		ctx, cancel := context.WithCancel(ctxV)
		return ctx, cancel
	}
	ctx, cancel := context.WithTimeout(ctxV, timeout)
	return ctx, cancel
}
