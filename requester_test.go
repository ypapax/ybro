package ybro

import (
	"github.com/stretchr/testify/require"
	"github.com/ypapax/logrus_conf"
	"log"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	if err := logrus_conf.PrepareFromEnv("ybro_test"); err != nil {
		log.Fatalf("error: %+v", err)
	}
	m.Run()
}

func TestHeadlessBrowser(t *testing.T) {
	r := require.New(t)
	host := os.Getenv("WEEK_HOST")
	r.NotEmpty(host)
	j := &Job{HeadlessBrowser: true, ShowBrowser: true, DontCloseBrowser: true, Url: host}
	_, err := HeadlessBrowser(j, 100*time.Second)
	r.NoError(err)
	r.NoError(Type(*j.Ctx, "#year-input", "hello world"))
	r.NoError(Click(*j.Ctx, ".btn-primary"))
	//r.NoError(ExpectTrueJs(*j.Ctx, `$("#submit-error").text().indexOf("Введите год, например ") == 0`, r))
	r.NoError(TextContains(*j.Ctx, "#submit-error", "Введите год, например ", r))
	j.CloseBro()
}
