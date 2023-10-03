package ybro

import (
	"bytes"
	"context"
	"fmt"
	"github.com/ypapax/yinline"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func getFullUrlChromeDP(ctx context.Context) (string, error) {
	t1 := time.Now()
	fullUrlTimeout := 20 * time.Second
	ctxFullUrl, cancel := GenerateTimeoutContextWithCancel(ctx, fullUrlTimeout)
	defer func() {
		cancel()
	}()
	var res string
	select {
	case <-ctx.Done():
		// return "", errors.Errorf("context is already canceled %+v", ctx)
		return "", nil
	default:
	}
	if err1 := chromedp.Location(&res).Do(ctxFullUrl); err1 != nil {
		screenshotFileName, err := ChromeDPScreenshot(ctx)
		if err != nil {
			logrus.Errorf("couldn't make a screenshot %+v", yinline.InlineErr(err))
			screenshotFileName += " couldn't make a screenshot"
		}

		return "", errors.Wrapf(err1, "screenshotFileName: %+v, with timeout %+v, time spent 99: %+v, ctx: %+v", screenshotFileName, fullUrlTimeout, time.Since(t1), ctx)
	}
	return res, nil
}

var (
	chromeDPScreenshotDir    string
	chromeDPScreenshotDirMtx sync.Mutex
)

func prepareScreenshotDir() (string, error) {
	chromeDPScreenshotDirMtx.Lock()
	defer chromeDPScreenshotDirMtx.Unlock()
	if len(chromeDPScreenshotDir) > 0 {
		return chromeDPScreenshotDir, nil
	}
	chromeDPScreenshotDir = fmt.Sprintf("/tmp/%+v", time.Now().Format(TimeFormatForFileName))
	if err := os.MkdirAll(chromeDPScreenshotDir, 0o777); err != nil {
		return chromeDPScreenshotDir, errors.WithStack(err)
	}
	return chromeDPScreenshotDir, nil
}

const (
	CancelCtxText         = "canceling the context"
	CtxIsCancelledText    = "context is already canceled %+v"
	TimeFormatForFileName = "2006-01-02T15_04_05.999999999Z07_00"
	ForJobLog             = "for job %+v"
)

func ChromeDPScreenshot(ctx context.Context) (string, error) {
	screenshotContext, cancel := context.WithTimeout(ctx, time.Minute)
	defer func() {
		logrus.Tracef(CancelCtxText)
		cancel()
	}()
	var buf []byte
	if err := chromedp.Run(screenshotContext, fullChromeDPScreenshot(90, &buf)); err != nil {
		return "", errors.WithStack(err)
	}
	dir, err := prepareScreenshotDir()
	if err != nil {
		return "", errors.WithStack(err)
	}
	screenshotFileName := fmt.Sprintf(dir+"/screenshot_%+v.png", time.Now().Format(TimeFormatForFileName))
	if err := os.WriteFile(screenshotFileName, buf, 0o644); err != nil {
		logrus.Errorf("couldn't make a screenshot: %+v", err)
		// screenshotFileName = "couldn't make a screenshot"
	}
	return screenshotFileName, nil
}

func fullChromeDPScreenshot(quality int, res *[]byte) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.FullScreenshot(res, quality),
	}
}

func getFullUrlChromeDPViaJs(ctx context.Context) (string, error) {
	fullUrlTimeout := 3 * time.Second
	ctxFullUrl, cancel := GenerateTimeoutContextWithCancel(ctx, fullUrlTimeout)
	defer func() {
		logrus.Tracef(CancelCtxText)
		cancel()
	}()
	js := "window.location.href"
	l := logrus.WithField("js", js)
	var res string
	l.Tracef("running js: %+v", js)
	if err1 := chromedp.Evaluate(js, &res).Do(ctxFullUrl); err1 != nil {
		return "", errors.Wrapf(err1, `for js: "%+v"`, js)
	}
	return res, nil
}

func getAllSelectsValuesExpr(ctx context.Context, iframeNumber int) (string, error) {
	prep := prepareIframeNumber(iframeNumber)
	js := prep +
		`var vals = ["` + prep + `"];
var select = doc.getElementsByTagName("select");
vals.push("/* selects count: "+select.length+"*/");
var optionsTotalCount = 0;
for (var i = 0; i < select.length; i++) {
    var selectedOptionValue = select[i].value;
    var id=select[i].id;
    var len = select[i].options.length;
    if (optionsTotalCount == 0) {
      optionsTotalCount=len;
      /* console.log("optionsTotalCount", optionsTotalCount); */
    } else if (len > 0) {
      optionsTotalCount*=len;
      /* console.log("optionsTotalCount", optionsTotalCount); */
    }
    if (!select[i].options[select[i].selectedIndex]) {
      continue;
    }
    var text=select[i].options[select[i].selectedIndex].text;
    vals.push("var el=doc.getElementById('"+id+"'); el.value = '"+selectedOptionValue+"'; /* option text: '"+text+"',options count: "+ len +"  */ el.dispatchEvent(new Event('change'));");
}
vals.push("/*total options count: "+optionsTotalCount+"*/");
vals.join(";");`
	l := logrus.WithField("js", js)
	var res string
	l.Tracef("running js: %+v", js)
	if err1 := RunJsCrhomeDpRetry(ctx, js, &res); err1 != nil {
		l.Errorf("%+v", err1)
		return "", errors.Wrapf(err1, `for js: "%+v"`, js)
	}
	res = strings.TrimSpace(res)
	if len(res) == 0 {
		// utils.File
		fileName := SaveFileDebugExt(js, "js")
		return "", errors.Errorf("missing sels state value for js: %+v, js content is saved to file %+v",
			yinline.InlineSep(js, " "), fileName)
	}
	l.Tracef("res value: %+v", res)
	return res, nil
}

const (
	scrolled    = "scrolled"
	notScrolled = "not scrolled"
)

func scrollToID(ctx context.Context, idOrClass sel, iframeNumber int) (bool, error) {
	prep := prepareIframeNumber(iframeNumber)

	if len(idOrClass.Id) == 0 && len(idOrClass.Class) == 0 {
		return false, errors.Errorf("missing both id and class")
	}
	el, err := elByIdOrClass(idOrClass)
	if err != nil {
		return false, errors.WithStack(err)
	}

	js := prep +
		fmt.Sprintf(`
%+v
if (el && el.length) {
	el.scrollIntoView(true);
	"%s";
} else {
	"%s";
}
`, el, scrolled, notScrolled)
	// l := logrus.WithField("js", js)
	var ok string
	// logrus.Tracef("running js: %+v", js)
	ok, err1 := runJsCrhomeDpRetryPossibleResults(ctx, js, []string{scrolled, notScrolled})
	if err1 != nil {
		return false, errors.Wrapf(err1, `for js: "%+v"`, js)
	}
	return ok == scrolled, nil
}

func scrollToIDOrElement(ctx context.Context, idOrClass sel, element string, iframeNumber int) (bool, error) {
	scrolledResult, err1 := scrollToID(ctx, idOrClass, iframeNumber)
	if err1 != nil {
		return false, errors.WithStack(err1)
	}
	if scrolledResult {
		return true, nil
	}
	scrolledResult2, err2 := scrollToElement(ctx, element, iframeNumber)
	if err2 != nil {
		return false, errors.WithStack(err2)
	}
	if scrolledResult2 {
		return true, nil
	}
	return false, nil
}

func scrollToElement(ctx context.Context, elementTagName string, iframeNumber int) (bool, error) {
	prep := prepareIframeNumber(iframeNumber)
	js := prep +
		fmt.Sprintf(`
var els = doc.getElementsByTagName("%+v");
if (els.length) {
	els[0].scrollIntoView(true)
	"%+v";
} else {
	"%+v"
}
`, elementTagName, scrolled, notScrolled)
	// l := logrus.WithField("js", js)
	var ok string
	// l.Tracef("running js: %+v", js)
	if err1 := RunJsCrhomeDpRetry(ctx, js, &ok); err1 != nil {
		// l.Errorf("%+v", err1)
		return false, errors.Wrapf(err1, `for js: "%+v", ctx: %+v`, js, ctx)
	}
	switch ok {
	case scrolled:
		return true, nil
	case notScrolled:
		return false, nil
	default:
		return false, errors.Errorf("'%+v' is unexpected result for js run: %+v", ok, yinline.InlineSep(js, " "))
	}
}

const htmlIsNotChangedMessage = "Html is not yet changed"

func RunJsCrhomeDpRetry(ctx0 context.Context, js string, res interface{}) (finalErr error) {
	logrus.Tracef("RunJsCrhomeDpRetry state started")
	defer func() {
		logrus.Tracef("RunJsCrhomeDpRetry state finished")
	}()
	t1 := time.Now()
	defer func() {
		if finalErr != nil {
			logrus.Warnf("time spent in RunJsCrhomeDpRetry %+v with error: %+v", time.Since(t1), yinline.InlineErr(finalErr))
		}
	}()
	runJsCrhomeDpRetryTimeout := 5 * time.Minute
	ctx, cancel := GenerateTimeoutContextWithCancel(ctx0, runJsCrhomeDpRetryTimeout)
	defer func() {
		cancel()
	}()
	fullUrl, err0 := getFullUrlChromeDP(ctx)
	if err0 != nil {
		return errors.Wrapf(err0, "time spent in : %+v, timeout %+v", time.Since(t1), runJsCrhomeDpRetryTimeout)
	}
	const errorIsCatchedStr = `error is catched`
	tryCatchJs := fmt.Sprintf(`try {
	%+v
} catch (e) {
	"%+v: "+e.stack
}`, js, errorIsCatchedStr)
	if err1 := Do(func() error {
		logrus.Tracef("running js %+v", yinline.InlineSep(js, " "))
		if err44 := chromedp.EvaluateAsDevTools(tryCatchJs, &res).Do(ctx); err44 != nil {
			return errors.Wrapf(err44, `for js: "%+v" and fullUrl: %+v`, js, fullUrl)
		}
		errorIsCatched := func() bool {
			resStr, ok := res.(string)
			if !ok {
				return false
			}
			if strings.Contains(resStr, errorIsCatchedStr) {
				return true
			}
			return false
		}()
		if errorIsCatched {
			return errors.Errorf("error is catched: %+v", res)
		}
		return nil
	}, 100*time.Millisecond, 30*time.Second); err1 != nil {
		screenshotFilePath, err := ChromeDPScreenshot(ctx)
		if err != nil {
			logrus.Errorf("screenshot error %+v", yinline.InlineErr(err))
		}
		return errors.Wrapf(err1, "screenshotFilePath: %+v, for js: %+v and url: %+v", screenshotFilePath, js, fullUrl)
	}
	return nil
}

func runJsCrhomeDpRetryPossibleResults(ctx context.Context, js string, validResults []string) (string, error) {
	var res string
	if err1 := Do(func() error {
		var err error
		res, err = runJsCrhomeDpPossibleResults(ctx, js, validResults)
		if err != nil {
			return errors.WithStack(err)
		}
		return nil
	}, 1000*time.Millisecond, 10*time.Second); err1 != nil {
		fullUrl, err2 := getFullUrlChromeDP(ctx)
		if err2 != nil {
			return "", errors.Wrapf(err2, "initial err: %+v", err1)
		}
		return res, errors.Wrapf(err1, "js: %+v   fullUrl: %+v", yinline.InlineSep(js, " "), fullUrl)
	}
	return res, nil
}

func runJsCrhomeDpPossibleResults(ctx context.Context, js string, validResults []string) (string, error) {
	fullUrl, err0 := getFullUrlChromeDP(ctx)
	if err0 != nil {
		return "", err0
	}
	var res string
	/*consoleLogJs := fmt.Sprintf(`console.log("%+v")`, js)

	if err44 := chromedp.Evaluate(consoleLogJs, &res).Do(ctx); err44 != nil {
		return "", errors.Wrapf(err44, `for js: "%+v" and fullUrl: %+v`, js, fullUrl)
	}*/
	logrus.Tracef("running js: %+v", yinline.InlineSep(js, " "))
	if err44 := chromedp.Evaluate(js, &res).Do(ctx); err44 != nil {
		return "", errors.Wrapf(err44, `for js: "%+v" and fullUrl: %+v`, js, fullUrl)
	}
	if len(res) == 0 {
		return "", errors.Errorf("missing result for js: %+v and url: %+v", js, fullUrl)
	}
	if len(validResults) == 0 {
		return res, nil
	}
	for _, validRes := range validResults {
		if validRes == res {
			return res, nil
		}
	}
	return res, errors.Errorf("result '%+v' is not valid for jsInline: %+v and url %+v, full js with new lines: %+v", res, yinline.InlineSep(js, " "), fullUrl, js)
}

func submitClosestUpperFormToElementID(ctx context.Context, id sel, iframeNumber int) error {
	prep := prepareIframeNumber(iframeNumber)
	el, err := elByIdOrClass(id)
	if err != nil {
		return errors.WithStack(err)
	}
	js := prep +
		fmt.Sprintf(`
try {
	%+v
	if (!el) {
		'el is not found'
    } else {
		var form = el.closest("form");
		if (form && form.length) {
			form.submit();
			'form found';
		} else {
			'form is not found';
		}
	}
} catch (ex) {
	ex.message;
}
`, el)
	l := logrus.WithField("js", js)
	l.Tracef("running js: %+v", js)
	_, err1 := runJsCrhomeDpRetryPossibleResults(ctx, js, []string{"form found"})
	if err1 != nil {
		l.Errorf("%+v", err1)
		return errors.Wrapf(err1, `for js: "%+v"`, js)
	}
	/*if !ok {
		err1 := errors.Errorf("unexpected result for js run: %+v", yinline.InlineSep(js, " "))
		fullUrl, err2 := getFullUrlChromeDP(ctx)
		if err2 != nil {
			return errors.Wrapf(err2, "initial error: %+v", err1)
		}
		return errors.Wrapf(err1, "fullUrl: %+v", fullUrl)
	}*/
	return nil
}

func iframeHtmlForNoIframe(ctx context.Context, iframeNumber int) (string, error) {
	js := prepareIframeNumber(iframeNumber) +
		`doc.documentElement.innerHTML;`
	t1 := time.Now()

	var res string
	/*sl := 2 * time.Second
	l.Tracef("running js with %+v sleep", sl)
	time.Sleep(sl)*/
	if err1 := RunJsCrhomeDpRetry(ctx, js, &res); err1 != nil {
		return "", errors.Wrapf(err1, `for js: "%+v", time spent: %+v, ctx: %+v`, js, time.Since(t1), ctx)
	}

	return res, nil
}

func iframeHtml(ctx context.Context, iframeNumber int) (resultHtml string, resultErr error) {
	l := logrus.WithField("iframeNumber", iframeNumber)
	defer func() {
		fileName := SaveFileDebugIframe(resultHtml, "", &iframeNumber)
		// l.Tracef("file is saved %+v, stack: %+v", fileName, spew.InlineSep(string(debug.Stack()), "; "))
		l.Tracef("file is saved %+v for iframe %+v", fileName, iframeNumber)
	}()
	h, err := iframeHtmlForNoIframe(ctx, iframeNumber)
	if err != nil {
		return "", errors.WithStack(err)
	}
	return h, nil

	/*if iframeNumber == noIframeIndex {
		h, err := iframeHtmlForNoIframe(ctx, iframeNumber)
		if err != nil {
			return "", errors.WithStack(err)
		}
		return h, nil
	}
	h, err := iframeHtmlForIframe(ctx, iframeNumber)
	if err != nil {
		return "", errors.WithStack(err)
	}
	return h, nil*/
}

func getAllDocumentHtmlWithRetryToWaitWhenLoaded(ctx context.Context, iframeIndex int) (string, error) {
	var html string
	if err := Do(func() error {
		var err error
		html, err = iframeHtml(ctx, iframeIndex)
		if err != nil {
			return errors.WithStack(err)
		}
		return nil
	}, time.Second, 20*time.Second); err != nil {
		return "", errors.WithStack(err)
	}
	return html, nil
}

func getAllDocumentHtml(ctx context.Context) (string, error) {
	fullUrl, err1 := getFullUrlChromeDP(ctx)
	if err1 != nil {
		return "", errors.WithStack(err1)
	}
	node, err := dom.GetDocument().Do(ctx)
	if err != nil {
		return "", errors.WithStack(err)
	}
	html, err := dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "fullUrl: %+v", fullUrl)
	}
	return strings.TrimSpace(html), nil
}

type Option struct {
	Text  string
	Value string
}

type sel struct {
	Id            string
	Class         string
	OptionsValues []Option
	ChoosedValue  *Option
}

func elByIdOrClass(idOrClass sel) (string, error) {
	if len(idOrClass.Id) == 0 && len(idOrClass.Class) == 0 {
		return "", errors.Errorf("missing both id and class for %+v", idOrClass)
	}
	el := fmt.Sprintf(`var el = doc.getElementById("%+v");`,
		idOrClass.Id)
	if len(idOrClass.Id) == 0 {
		el = fmt.Sprintf(`
var els = doc.getElementsByClassName("%+v");
var el = els[0];
`, idOrClass.Class)
	}
	return el, nil
}

func RemoveHeaders(d *goquery.Document) {
	headerClass := []string{".layout_header", ".header"}
	for _, hc := range headerClass {
		d.Find(hc).Each(func(i int, selection *goquery.Selection) {
			html, err := selection.Html()
			if err != nil {
				logrus.Errorf("couldn't get html %+v", errors.WithStack(err))
			}
			logrus.Tracef("found class %+v, gonna remove it: %+v", hc, yinline.InlineSep(html, " "))
		})
		d.Find(hc).Remove()
	}
}

/* not working
func getAllIframeContexts(ctx context.Context) []context.Context {
	targets, _ := chromedp.Targets(ctx)
	var result []context.Context
	logrus.Tracef("targets: %+v ", len(targets))
	for i, t := range targets {
		li := logrus.WithField("i", i)
		li.Tracef("t.Type: %+v, t: %+v", t.Type, *t)
		if t.Type != "iframe" {
			continue
		}
		ictx, _ := chromedp.NewContext(ctx, chromedp.WithTargetID(t.TargetID))
		logrus.Tracef("found iframe: t: %+v ", *t)
		result = append(result, ictx)
	}
	logrus.Tracef("iframes count: %+v ", len(result))
	return result
}*/

/* not working - hanging
func iframesList(ctx context.Context) error {
	logrus.Tracef("before second iframes len")
	var iframes []*cdp.Node
	if err := chromedp.Nodes(`iframe`, &iframes, chromedp.ByQuery).Do(ctx); err != nil {
		return errors.WithStack(err)
	}
	logrus.Tracef("second iframes len: %+v", len(iframes))
	return nil
}*/

func iframesCount(ctx context.Context) (int, error) {
	js := `var htmls = [];
var iframes = document.getElementsByTagName("iframe");
iframes.length;`
	l := logrus.WithField("js", js)
	var res int
	l.Tracef("running js")
	if err1 := RunJsCrhomeDpRetry(ctx, js, &res); err1 != nil {
		return 0, errors.Wrapf(err1, `for js: "%+v"`, js)
	}
	return res, nil
}

func getIframesUrls(html string) ([]string, error) {
	r := bytes.NewReader([]byte(html))
	d, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var iframeSrs []string
	d.Find("iframe").Each(func(i int, selection *goquery.Selection) {
		src, ok := selection.Attr("src")
		if !ok {
			logrus.Warnf("missing iframe src")
			return
		}
		if strings.Contains(src, "https://www.google.com/recaptcha") {
			logrus.Warnf("ignore this iframe")
			return
		}
		iframeSrs = append(iframeSrs, src)
	})

	return iframeSrs, nil
}

func chromeTask0(chromeContext0 context.Context, requestHeaders map[string]interface{}, response *string, statusCode *int64, contentType *string, responseHeaders *map[string]interface{}, job *Job) chromedp.Tasks {
	url := job.Url
	// timeout := 31 * time.Minute
	l := logrus.WithField("url", url) /*.WithField("timeout", timeout)*/
	l.Tracef("starting")
	/*chromeContext, chromeContextCancel := GenerateTimeoutContextWithCancel(chromeContext0, timeout)
	if !job.DontCloseBrowser {
		defer chromeContextCancel()
	}*/
	l.Tracef("trace here")

	chromedp.ListenTarget(chromeContext0, func(event interface{}) {
		l.Tracef("Listen Target")
		switch responseReceivedEvent := event.(type) {
		case *network.EventResponseReceived:
			response := responseReceivedEvent.Response
			if response.URL == url || response.URL == UrlWithoutHash(url) {
				*statusCode = response.Status
				*responseHeaders = response.Headers
				var err error
				*contentType, err = GetContentType(response.Headers, false)
				if err != nil {
					logrus.Errorf("couldn't parse content type %+v", errors.Wrapf(err, "for url: %+v, initial url: %+v, headers: %+v", response.URL, url, response.Headers))
				}

				lb := l.WithField("response.URL", response.URL).WithField("statusCode", *statusCode).
					WithField("responseHeaders", responseHeaders).
					WithField("contentType", *contentType).WithField("UrlWithoutHash(url)", UrlWithoutHash(url)).
					WithField("response.URL == UrlWithoutHash(url)", response.URL == UrlWithoutHash(url)).
					WithField("url", url)
				lb.Tracef("got headers and status")
			}
			lc := l.WithField("response.URL", "response.URL: "+response.URL).
				WithField("response.Status", fmt.Sprintf("status: %+v", response.Status))
			lc.Tracef("EventResponseReceived")
		case *network.EventRequestWillBeSent:
			request := responseReceivedEvent.Request
			l.Tracef("chromedp is requesting url (could be in background): %s\n", request.URL)
			if responseReceivedEvent.RedirectResponse != nil {
				from := responseReceivedEvent.RedirectResponse.URL
				to := request.URL
				if from == url {
					url = to
					l.Tracef(" got redirect: from %+v to %s", from, to)
				}
			}
		}
	})
	l.Tracef("here trace")
	return chromedp.Tasks{
		network.Enable(),
		network.SetExtraHTTPHeaders(requestHeaders),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny).WithDownloadPath("."),
		chromedp.ActionFunc(func(ctx context.Context) error {
			l.Tracef("ActionFunc")
			timeoutVal := 10 * time.Second
			navigateCtx, navigateCancel := GenerateTimeoutContextWithCancel(ctx, timeoutVal)
			defer func() {
				logrus.Tracef(CancelCtxText)
				navigateCancel()
			}()
			if err0 := chromedp.Navigate(url).Do(navigateCtx); err0 != nil {
				return errors.Wrapf(err0, "url: %+v and timeout %+v", url, timeoutVal)
			}
			l.Tracef("ActionFunc")
			if len(*contentType) > 0 {
				if !HtmlContentType(*contentType) {
					l.Infof("(not) skipping for now this url because it has not interesting content type: '%+v'",
						*contentType)
					// return nil
				}
			}
			return nil
		}),
	}
}

func chromeTask(chromeContext0 context.Context, requestHeaders map[string]interface{}, response *string, statusCode *int64, contentType *string, responseHeaders *map[string]interface{}, job *Job) chromedp.Tasks {
	url := job.Url
	l := logrus.WithField("url", url)
	// timeout := 31 * time.Minute
	l.Tracef("starting")
	/*chromeContext, chromeContextCancel := GenerateTimeoutContextWithCancel(chromeContext0, timeout)
	if !job.DontCloseBrowser {
		defer chromeContextCancel()
	}*/
	l.Tracef("trace here")
	if chromeContext0 == nil {
		panic("chromeContext0 is nil")
	}
	chromedp.ListenTarget(chromeContext0, func(event interface{}) {
		// l.Tracef("Listen Target")
		switch responseReceivedEvent := event.(type) {
		case *network.EventResponseReceived:
			response := responseReceivedEvent.Response
			if response.URL == url || response.URL == UrlWithoutHash(url) {
				*statusCode = response.Status
				*responseHeaders = response.Headers
				var err error
				*contentType, err = GetContentType(response.Headers, false)
				if err != nil {
					logrus.Errorf("couldn't parse content type %+v", errors.Wrapf(err, "for url: %+v, initial url: %+v, headers: %+v", response.URL, url, response.Headers))
				}

				lb := l.WithField("response.URL", response.URL).WithField("statusCode", *statusCode).
					WithField("responseHeaders", responseHeaders).
					WithField("contentType", *contentType).WithField("UrlWithoutHash(url)", UrlWithoutHash(url)).
					WithField("response.URL == UrlWithoutHash(url)", response.URL == UrlWithoutHash(url)).
					WithField("url", url)
				lb.Tracef("got headers and status")
			}
			// lc := l.WithField("response.URL", "response.URL: "+response.URL).
			//	WithField("response.Status", fmt.Sprintf("status: %+v", response.Status))
			// lc.Tracef("EventResponseReceived")
		case *network.EventRequestWillBeSent:
			request := responseReceivedEvent.Request
			// l.Tracef("chromedp is requesting url (could be in background): %s\n", request.URL)
			if responseReceivedEvent.RedirectResponse != nil {
				from := responseReceivedEvent.RedirectResponse.URL
				to := request.URL
				if from == url {
					url = to
					l.Tracef(" got redirect: from %+v to %s", from, to)
					if job.DetectRedirectUrl != nil {
						*job.DetectRedirectUrl = to
					}
				}
			}
		}
	})
	l.Tracef("here trace")
	return chromedp.Tasks{
		network.Enable(),
		network.SetExtraHTTPHeaders(requestHeaders),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny).WithDownloadPath("."),
		chromedp.ActionFunc(func(ctx context.Context) error {
			defer func() {
				l.Tracef("chromedp.ActionFunc is finished")
			}()
			l.Tracef("ActionFunc")
			/*timeout := 10 * time.Second
			chromedp.NewContext()
			navigateCtx, navigateCancel := GenerateTimeoutContextWithCancel(ctx, timeout)
			if !job.DontCloseBrowser {
				defer navigateCancel()
			}*/
			if err0 := chromedp.Navigate(url).Do(ctx); err0 != nil {
				if ErrContains(err0, "net::ERR_NAME_NOT_RESOLVED") {
					logrus.Warnf("%+v", err0)
					return nil
				}
				return errors.Wrapf(err0, "url: %+v", url)
			}
			sl := 10 * time.Second
			l.Tracef("sleeping for %+v", sl)
			time.Sleep(sl)
			resp, err := getAllDocumentHtmlWithRetryToWaitWhenLoaded(ctx, noIframeIndex)
			if err != nil {
				return errors.Wrapf(err, "for url %+v", url)
			}
			l.Tracef("ActionFunc2")
			if len(*contentType) > 0 {
				if !HtmlContentType(*contentType) {
					l.Infof("(not) skipping for now this url because it has not interesting content type: '%+v'",
						*contentType)
					// return nil
				}
			}
			*response = resp
			return nil
		}),
	}
}

const noIframeIndex = -1

func prepareIframeNumber(iframeNumber int) string {
	if iframeNumber == noIframeIndex {
		return "var doc = document; "
	}
	return fmt.Sprintf(
		`var doc = document.getElementsByTagName('iframe')[%d].contentWindow.document; `,
		iframeNumber)
}

func optionValuesBySelectId(ctx context.Context, selectID string, iframeNumber int) ([]string, error) {
	js := prepareIframeNumber(iframeNumber) +
		fmt.Sprintf(`var select = doc.getElementById("%+v");
var optionsVals = [];
for (var i = 0; i < select.options.length; i++) {
	optionsVals.push(select.options[i].value)
}
optionsVals;`, selectID)
	l := logrus.WithField("js", js)
	var res []string
	l.Tracef("running js")
	if err1 := RunJsCrhomeDpRetry(ctx, js, &res); err1 != nil {
		return nil, errors.Wrapf(err1, `for js: "%+v"`, js)
	}
	return res, nil
}
