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

	ctx, closeBro := Goto(host, 2*time.Minute, true, r)
	Type(ctx, "#year-input", "hello world", r)
	Click(ctx, ".btn-primary", r)
	TextContains(ctx, "#submit-error", "Введите год, например ", r)
	closeBro()
}
