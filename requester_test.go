package ybro

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestHeadlessBrowser(t *testing.T) {
	j := &Job{HeadlessBrowser: true, ShowBrowser: true, DontCloseBrowser: true, Url: "https://google.com"}
	_, err := HeadlessBrowser(j, 30*time.Second)
	r := require.New(t)
	r.NoError(err)
	r.NoError(Type(j.Ctx, "textarea", "hello world"))
}
