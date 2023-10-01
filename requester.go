package ybro

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chromedp/chromedp"
	"github.com/moul/http2curl"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Result struct {
	Job                     Job
	Body                    []byte
	Error                   error
	StatusCode              int
	Header                  http.Header
	Response                *http.Response
	Proxy                   string
	FileDownloadIsCancelled bool
	ContentType             string
	ChromeUsed              bool
}

func (r *Result) String() string {
	return fmt.Sprintf("status: %+v, Proxy: %+v, ChromeUsed: %+v, job: %+v", r.StatusCode, r.Proxy, r.ChromeUsed, r.Job)
}

func GetContentTypeHeader(hh http.Header, throwErrIfContentTypeMissing bool) (string, error) {
	hh2 := make(map[string]interface{})
	for k, vv := range hh {
		if len(vv) == 0 {
			continue
		}
		hh2[k] = vv[0]
	}
	ct, err := GetContentType(hh2, throwErrIfContentTypeMissing)
	if err != nil {
		return "", errors.WithStack(err)
	}
	return ct, nil
}

type Job struct {
	Url                                        string
	Method                                     string
	Payload                                    []byte
	Headers                                    map[string]string
	RetryIfError                               time.Duration
	SleepInCaseOfError                         time.Duration
	Type                                       string
	CurlStr                                    string
	Info                                       string
	HeadlessBrowser                            bool
	AddNewRelicError                           bool
	Delay                                      time.Duration
	UseProxy                                   bool
	LockBetweenReqs                            time.Duration
	LockBetweenReqsRandomizerMultiplierPercent int
	ShowBrowser                                bool
	DontCloseBrowser                           bool
	DetectRedirectUrl                          *string
	WaitAfterChooseSelector                    time.Duration
	DontReadBodyIfNotHtmlContentType           bool

	Label string
	Ctx   context.Context
}

func (j Job) String() string {
	return strings.Join([]string{j.Type, j.Info, j.CurlStr, j.Url, fmt.Sprintf("UseProxy: %+v, LockBetweenReqs: %s, Label: %+v", j.UseProxy, j.LockBetweenReqs, j.Label)}, "  :  ")
}

var reqMtx = sync.Mutex{}

func init() {
	rand.Seed(time.Now().Unix())
}

func Request(job *Job, requestTimeout time.Duration) (finalResult *Result, finalErr error) {
	if len(job.Url) == 0 {
		return nil, errors.Errorf("missing url to request")
	}
	t1 := time.Now()
	job.AddNewRelicError = true
	method := strings.ToUpper(job.Method)
	lo := logrus.WithField("method", method).
		WithField("url", job.Url).
		WithField("req-delay", job.Delay.String()).
		WithField("LockBetweenReqs", job.LockBetweenReqs.String()).
		WithField("LockBetweenReqsRandomizerMultiplierPercent", fmt.Sprintf("%d", job.LockBetweenReqsRandomizerMultiplierPercent)).
		WithField("job-info", job.Info).WithField("job.HeadlessBrowser", job.HeadlessBrowser)
	defer func() {
		if finalErr != nil {
			loStr, err2 := lo.String()
			if err2 != nil {
				logrus.Errorf("couldn't get str from logrus %+v", errors.WithStack(err2))
			}
			finalErr = errors.Wrapf(finalErr, "loStr: %+v", loStr)
			lo.Infof("req is finished with error")
			return
		}
		lo.Infof("req is finished successfully: %+v", time.Since(t1))
	}()
	if job.Delay > 0 {
		lo.Tracef("sleep for delay job.Delay %s", job.Delay)
		time.Sleep(job.Delay)
	}
	if job.LockBetweenReqs > 0 {
		lo.Tracef("waiting for the lock job.LockBetweenReqs")
		reqMtx.Lock()
		lo.Tracef("got the the lock job.LockBetweenReqs")
		defer func() {
			go func() {
				sl := job.LockBetweenReqs
				if job.LockBetweenReqsRandomizerMultiplierPercent > 0 {
					r := rand.Int31n(int32(job.LockBetweenReqsRandomizerMultiplierPercent))
					delta := sl * time.Duration(r) / 100

					if delta > 0 {
						const amountOfSigns = 2
						sign := rand.Int31n(amountOfSigns)
						if sign == amountOfSigns-1 {
							sl += delta
						} else {
							sl -= delta
						}
					}
				}
				lo.Tracef("sleep for job.LockBetweenReqs %s", sl)
				time.Sleep(job.LockBetweenReqs)
				reqMtx.Unlock()
				lo.Tracef("unlocked the lock job.LockBetweenReqs")
			}()
		}()
	}
	if job.HeadlessBrowser && (method == "GET" || len(method) == 0) {
		lo.Tracef("headless request")
		r, err := HeadlessBrowser(job, requestTimeout)
		if err != nil {
			time.Sleep(job.SleepInCaseOfError)
			return r, errors.Wrapf(err, "slept for %s after error", job.SleepInCaseOfError)
		}
		return r, nil
	}
	lo.Tracef("job.HeadlessBrowser: %+v", job.HeadlessBrowser)
	r, err := Go(job, requestTimeout)
	if err != nil {
		lo.Tracef("minusing counter because of error: %+v", err)
		return r, errors.WithStack(err)
	}
	lo.Tracef("req is done")
	return r, nil
}

const StatusCode404Error = "not good status code 404"

func IsStatusCode404Error(err error) bool {
	return strings.Contains(fmt.Sprintf("%+v", err), StatusCode404Error)
}

func RequestRetry(job *Job, requestTimeout, overallTimeout, inCaseOfErrorDelay time.Duration) (*Result, error) {
	done := make(chan *Result)
	giveUp := make(chan bool)
	lo := logrus.WithField("overallTimeout", overallTimeout).
		WithField("requestTimeout", requestTimeout).
		WithField("inCaseOfErrorDelay", inCaseOfErrorDelay).
		WithField("go-func-caller-stack", string(debug.Stack()))
	var lastErr error

	type errResult struct {
		Result *Result
		Err    error
	}
	errResChan := make(chan errResult)
	go func() {
		attempt := 0
		for {
			select {
			case <-giveUp:
				lo.Tracef("give up")
				return
			default:
			}
			attempt++
			r, err := Request(job, requestTimeout)
			if err != nil {
				err = errors.Wrapf(err, "attempt: %+v", attempt)
				lastErr = err
				if r != nil {
					if r.StatusCode >= 400 && r.StatusCode <= 499 {
						errResChan <- errResult{
							r,
							errors.Wrapf(err, "don't retry for status code %+v", r.StatusCode),
						}
						return
					}
				}
				lo.Infof("error: %+v, will retry in %s", err, inCaseOfErrorDelay)
				time.Sleep(inCaseOfErrorDelay)
				continue
			}
			done <- r
			break
		}
	}()
	select {
	case <-time.After(overallTimeout):
		close(giveUp)
		return nil, errors.Errorf("overall timeout %s waiting for request CurlStr: %+v, Url: %+v,  timeout: %+v, job_delay: %+v, lastError: %+v, LockBetweenReqs: %+v", overallTimeout, job.CurlStr, job.Url, requestTimeout, job.Delay, lastErr, job.LockBetweenReqs)
	case r := <-done:
		return r, nil
	case errRes := <-errResChan:
		close(giveUp)
		return errRes.Result, errors.WithStack(errRes.Err)
	}
}

var DefaultHeaders = map[string]string{
	"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/81.0.4044.138 Safari/537.36",
	"Accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9",
}

func Response(job *Job, requestTimeout time.Duration) (*Result, error) {
	t1 := time.Now()
	client := &http.Client{
		Timeout: requestTimeout,
	}
	var prox string
	usedProxy := false

	req, err := http.NewRequest(job.Method, job.Url, bytes.NewBuffer(job.Payload))
	if err != nil {
		err := errors.Wrap(err, "couldn't create request")
		return nil, err
	}
	req.Close = true
	for k, v := range job.Headers {
		req.Header.Add(k, v)
	}
	curlCmd, toCurlErr := http2curl.GetCurlCommand(req)
	if toCurlErr != nil {
		logrus.Error(toCurlErr)
	}
	job.CurlStr = curlCmd.String()
	if job.UseProxy {
		job.CurlStr = strings.Replace(job.CurlStr, "curl ", "curl --proxy "+prox+" ", 1)
	}
	l := logrus.WithField("job-info", job.Info)

	logrus.Infof("requesting %+v", job)
	res, err := client.Do(req)
	defer func() {
		logrus.Infof("request is finished for %s: %+v", time.Since(t1), job)
	}()
	if err != nil {
		job.AddNewRelicError = false
		err = errors.Wrapf(err, "couldn't make request for req %s and timeout: %s, proxy: %+v, usedProxy: %+v, time spent: %+v",
			job.CurlStr, requestTimeout, prox, usedProxy, time.Since(t1))
		l.Tracef("error: %+v", err)
		return nil, err
	}
	l.Tracef("request is done: %+v", res.StatusCode)

	if res.StatusCode > 399 || res.StatusCode < 200 {
		var bodyText string
		defer res.Body.Close()
		b, err2 := ioutil.ReadAll(res.Body)
		if err2 != nil {
			bodyText = fmt.Sprintf("couldn't get body: %+v", err2)
		} else {
			bodyText = string(b)
		}
		const maxBodyTextChars = 2500
		var bodyTextForErr string
		if utf8.RuneCountInString(bodyText) > maxBodyTextChars {
			bodyTextForErr = string([]rune(bodyText)[:maxBodyTextChars])
		}
		err := errors.WithStack(fmt.Errorf("not good status code %+v requesting %+v, bodyTextForErr: %+v", res.StatusCode, job, bodyTextForErr))
		return &Result{Job: *job, Body: []byte(bodyText), StatusCode: res.StatusCode, Header: res.Header, Response: res}, err
	}
	ct, err := GetContentTypeHeader(res.Header, false)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &Result{Job: *job, StatusCode: res.StatusCode, Header: res.Header, Proxy: prox, Response: res, ContentType: ct}, nil
}

func Go(job *Job, requestTimeout time.Duration) (r *Result, errResult error) {
	if job.Headers == nil {
		job.Headers = DefaultHeaders
	}
	lf := logrus.WithField("job", job).WithField("job.DontReadBodyIfNotHtmlContentType", job.DontReadBodyIfNotHtmlContentType)
	lf.Tracef("starting")
	t1 := time.Now()
	defer func() {
		lf.Tracef("req is done for %s", time.Since(t1))
	}()
	result, err := Response(job, requestTimeout)
	if err != nil {
		if result != nil {
			lf = lf.WithField("content-type", result.ContentType)
		}
		lf.Tracef("got error: %+v", err)
		return result, errors.WithStack(err)
	}
	if job.DontReadBodyIfNotHtmlContentType {
		lf.Tracef("start checking content type")
		if result == nil {
			return nil, errors.Errorf("result is nil: for job %+v", *job)
		}
		lf = lf.WithField("content-type", result.ContentType)
		lf.Tracef("before checking content type")
		if len(result.ContentType) == 0 {
			return result, errors.Errorf("missing content type: for job %+v and result: %+v", *job, *result)
		}
		if !HtmlContentType(result.ContentType) {
			lf.Tracef("don't read body becase it doesn't have html content type: %+v", result.ContentType)
			return result, nil
		}
	}
	defer result.Response.Body.Close()
	b, err := ioutil.ReadAll(result.Response.Body)
	if err != nil {
		err = errors.Wrapf(err, "couldn't read body, proxy: %+v", result.Proxy)
		return nil, err
	}
	if len(b) == 0 || len(b) == 0 {
		err := errors.Errorf(emptyBodyResp+" for requesting %s, status code: %+v, proxy: %+v", job.CurlStr, result.Response.StatusCode, result.Proxy)
		return &Result{Job: *job, Body: nil, StatusCode: result.Response.StatusCode, Header: result.Response.Header, ContentType: result.ContentType}, err
	}
	return &Result{Job: *job, Body: b, StatusCode: result.Response.StatusCode, Header: result.Response.Header, ContentType: result.ContentType}, nil
}

const emptyBodyResp = "empty body in response"

func IsEmptyBodyErr(err error) bool {
	return IsErr(err, emptyBodyResp)
}

func IsErr(ierr error, contains string) bool {
	return strings.Contains(fmt.Sprintf("%+v", ierr), contains)
}

type SelectHtml struct {
	Html         string
	Url          string
	SelectsState string
}

func HeadlessBrowser(job *Job, requestTimeout time.Duration) (finalResult *Result, finalErr error) {
	reuseContext := os.Getenv("REUSE_CHROME_CONTEXT") == "true"
	t1 := time.Now()
	reqTimeout := requestTimeout
	lf := logrus.WithField("requestTimeout", requestTimeout)
	lf.Infof("chrome req is starting")
	defer func() {
		if finalResult != nil {
			finalResult.ChromeUsed = true
		}
		lf.Infof("chrome req is done, time: %+v, finalErr: %+v", time.Since(t1), finalErr)
	}()
	done := make(chan bool)
	defer func() {
		lf.Tracef("before writing to done channel")
		select {
		case done <- true:
			lf.Tracef("after writing to done channel")
		default:
			lf.Tracef("nobody listening to channel done, alright")
		}
		lf.Tracef("this defer is done")
	}()
	go func() {
		timeoutPlusOneMinute := reqTimeout + time.Minute
		select {
		case <-done:
			lf.Tracef("chromeTask is done")
		case <-time.After(timeoutPlusOneMinute):
			logrus.Errorf("too slow for job %+v and timeoutPlusOneMinute %+v", job, timeoutPlusOneMinute)
		}
	}()

	// create a timeout

	defer func() {
		lf.Infof("HeadlessBrowser req is finished: %s, %+v, finalErr == nil: %+v",
			time.Since(t1), SpentLimits(t1, time.Second, 5*time.Minute, "browser req"), finalErr == nil)
	}()
	keyToKill := fmt.Sprintf("%+v--%+v", time.Now().Format(time.RFC3339Nano), rand.Int())
	myDefaultExecAllocatorOptions := [...]chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,

		// After Puppeteer's default behavior.
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("1url", job.Url),
		chromedp.Flag("3Label", job.Label),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-features", "site-per-process,TranslateUI,BlinkGenPropertyTrees"),
		chromedp.Flag("disable-hang-monitor", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("enable-automation", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("my-chromedp-runner", keyToKill),
	}
	opts := myDefaultExecAllocatorOptions[:]
	func() {
		if !job.UseProxy {
			return
		}
		proxyServer := os.Getenv("PROXY_SERVER")
		if len(proxyServer) == 0 {
			lf.Warnf("missing proxy from env: '%+v'", proxyServer)
			return
		}
		lf.Infof("using proxy for chrome dp: %+v", proxyServer)
		// create chrome instance
		opts = append(opts,
			// ... any options here
			chromedp.ProxyServer(proxyServer),
		)
	}()
	if !job.ShowBrowser {
		opts = append(opts, chromedp.Headless)
	}
	lf.Tracef("job.ShowBrowser: %+v", job.ShowBrowser)
	ctx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	job.Ctx = ctx
	/*if !job.DontCloseBrowser {
		defer cancelContext()
	}*/
	/*defer func(){
		if job.DontCloseBrowser {
			lf.Infof("skip running pkill and cancel func because of job.DontCloseBrowser")
			return
		}
		cancelFunc()
		if !stage.IsProdOrStaging() {
			lf.Infof("we don't run pkill locally")
			return
		}
		//keyToKill
		timeoutForKill := time.Minute
		cmdCtx, cmdCancel := context.WithTimeout(context.Background(), timeoutForKill)
		defer cmdCancel()
		var pkillArgs = []string{"-f", keyToKill}
		cmd := exec.CommandContext(cmdCtx, "pkill", pkillArgs...)
		var outb, errb bytes.Buffer
		cmd.Stdout = &outb
		cmd.Stderr = &errb
		errCmd := cmd.Run()
		if errCmd != nil {
			//alert.TrySendToNewRelic("couldn't run pkill command", errors.Errorf("couldn't run pkill command: %+v, stderr: %+v, stdout: %+v", strings.Join(cmd.Args, " "), errb.String(), outb.String()))
			logrus.Warnf("%+v", errors.Errorf("couldn't run pkill command: %+v, stderr: %+v, stdout: %+v", strings.Join(cmd.Args, " "), errb.String(), outb.String()))
		}
		lf.Infof("run command pkill command: %+v , stderr: %+v, stdout: %+v, errCmd: %+v", strings.Join(cmd.Args, " "), errb.String(), outb.String(), errCmd)
	}()*/
	lf.Tracef("before got ctxObjFromPool")
	newContext, cancelNewContext := chromedp.NewContext(
		ctx,
		chromedp.WithLogf(logrus.Tracef),
	)
	defer cancelNewContext()
	choosedChromeContext := newContext
	lf = lf.WithField("reuseContext", reuseContext)
	if reuseContext {
	} else {
		if !job.DontCloseBrowser {
			defer func() {
				lf.Tracef("trying to close context...")
				cancelTimeout := 60 * time.Second
				tctx, tcancel := context.WithTimeout(choosedChromeContext, cancelTimeout)
				defer func() {
					tcancel()
				}()
				if errCancel := chromedp.Cancel(tctx); errCancel != nil {
					logrus.Warnf("couldn't cancel a chrome context because of timeout: %+v: %+v", cancelTimeout, errCancel)
					return
				}
				lf.Infof("context is canceled successfully")
			}()
		}
	}
	lf.Tracef("got ctxObjFromPool")

	if choosedChromeContext == nil {
		panic("ctxFromPool is nil")
	}

	var response string
	var contentType string
	var statusCode int64
	var responseHeaders map[string]interface{}
	setContentTypeLogger := func(contentType string) {
		lf = lf.WithField("content-type", fmt.Sprintf("content-type: %+v", contentType))
	}
	lf.Tracef("requestTimeout: %+v", requestTimeout)
	ctxChromeWithTimeout, cancelGenerateTimeoutContextWithCancel := GenerateTimeoutContextWithCancel(choosedChromeContext, requestTimeout)
	if !job.DontCloseBrowser {
		defer cancelGenerateTimeoutContextWithCancel()
	}
	lf.Tracef("before chromedp.Run")
	if errR := chromedp.Run(ctxChromeWithTimeout,
		chromeTask(ctxChromeWithTimeout, map[string]interface{}{"User-Agent": "Mozilla/5.0"}, &response, &statusCode, &contentType, &responseHeaders, job),
	); errR != nil {
		lf.Tracef("chromeTask is finished with error: %+v", errR)
		setContentTypeLogger(contentType)
		const downloadFileCancelledTrace = "ERR_ABORTED"
		if strings.Contains(fmt.Sprintf("%+v", errR), downloadFileCancelledTrace) {
			lf.Tracef("trace is here")
			lf.Tracef("here responseHeaders: %+v", responseHeaders)
			lf = lf.WithField("download-file-is-canceled", true)
			lf.Tracef("returning resulttt")
			return &Result{Job: *job, ContentType: contentType, StatusCode: int(statusCode), FileDownloadIsCancelled: true}, nil
		}
		lf.Tracef("returning resulttt")
		return &Result{Job: *job, ContentType: contentType, StatusCode: int(statusCode), Body: []byte(response)}, errors.Wrapf(
			errR, "couldn't make a request for url %+v, time passed: %+v, ctxFromPool: %+v", job.Url, time.Since(t1), ctxChromeWithTimeout)
	}
	lf.Tracef("chromeTask is finished without error")
	lf = lf.WithField("status", statusCode).WithField("resp-headers", fmt.Sprintf("%+v",
		responseHeaders)).WithField("url", job.Url)
	if statusCode != 0 && (statusCode > 399 || statusCode < 200) {
		lf.Warnf("bad status code: %+v for url %+v", statusCode, job.Url)
		/*return &Result{Job: *job, ContentType: contentType, StatusCode: int(statusCode), Body: []byte(response)}, errors.Errorf(
		)*/
		return nil, nil
	}
	return &Result{Job: *job, ContentType: contentType, StatusCode: int(statusCode), Body: []byte(response)}, nil
	/*
		const chromeDPDir = "/tmp"
		if _, err := os.Stat(chromeDPDir); os.IsNotExist(err) {
			logrus.Infof("directory %+v doesn't exist, creating it...", chromeDPDir)
			if err := os.Mkdir(chromeDPDir, os.ModePerm); err != nil {
				return nil, errors.WithStack(err)
			}
		}

		// create chrome instance
		ctx, cancel := chromedp.NewContext(
			context.Background(),
			chromedp.WithLogf(log.Printf),
		)
		defer cancel()

		// create a timeout
		ctx, cancel = timeout.GenerateTimeoutContextWithCancel(ctx, requestTimeout)
		defer cancel()

		u := job.Url
		selector := `html`
		logrus.Tracef("requesting %+v selector: %+v", u, selector)
		var html string
		if job.UseProxy {
			proxyServer := proxy.ServerFromEnv()
			logrus.Infof("using proxy for chrome dp: %+v", proxyServer)
			chromedp.ProxyServer(proxyServer)
		} else {
			chromedp.ProxyServer("")
			logrus.Infof("no proxy for chrome dp")
		}
		if err := chromedp.Run(ctx,
			chromedp.Navigate(u),
			chromedp.WaitReady(selector),
			chromedp.OuterHTML(selector, &html),
		); err != nil {
			HeadlessCounter.Failed()
			return nil, errors.Wrapf(err, "counter: %s, requestTimeout: %s, freq conf: %+v, url: %+v", HeadlessCounter, requestTimeout, job.FreqConf, u)
		}
		HeadlessCounter.Ok()
		logrus.Tracef("headless reqs stats: %s, freq conf: %+v", HeadlessCounter, job.FreqConf)
		return &Result{Job: *job, Body: []byte(html)}, nil*/
}

func UrlWithoutQuery(u string) string {
	return strings.Split(u, "?")[0]
}
