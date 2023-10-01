package ybro

import (
	"fmt"
	"io/ioutil"

	"github.com/sirupsen/logrus"
)

func SaveFileDebug(content string) string {
	return SaveFileDebugIframe(content, "", nil)
}

func SaveFileDebugExt(content, extension string) string {
	return SaveFileDebugIframe(content, extension, nil)
}

func SaveFileDebugIframe(content, extension string, iframeIndex *int) string {
	if extension == "" {
		extension = "html"
	}
	var iframePart string
	if iframeIndex != nil {
		iframePart += fmt.Sprintf("_iframe_%+v", *iframeIndex)
	}
	filePath := fmt.Sprintf("/tmp/%+v%+v.%+v", len(content), iframePart, extension)
	if err := ioutil.WriteFile(filePath, []byte(content), 0o666); err != nil {
		logrus.Errorf("write debug file: %+v", err)
		return ""
	}
	return filePath
}
