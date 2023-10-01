package ybro

import (
	"strings"

	"github.com/pkg/errors"
)

func UrlWithoutHash(u string) string {
	return strings.Split(u, Hash)[0]
}

const Hash = "#"

func HtmlContentType(contentType string) bool {
	const requiredContentType = "text/html"
	if strings.Contains(strings.ToLower(contentType), strings.ToLower(requiredContentType)) {
		return true
	}
	return false
}

func GetContentType(hh map[string]interface{}, throwErrIfContentTypeMissing bool) (string, error) {
	const ctField = "Content-Type"
	hhLower := make(map[string]interface{})
	for k, v := range hh {
		hhLower[strings.ToLower(k)] = v
	}
	contentTypeObj, ok := hhLower[strings.ToLower(ctField)]
	if !ok {
		if !throwErrIfContentTypeMissing {
			return "", nil
		}
		return "", errors.Errorf("missing field '%+v' for headers: %+v", ctField, hh)
	}
	contentType, ok2 := contentTypeObj.(string)
	if !ok2 {
		return "", errors.Errorf("couldn't convert value to string: %+v", contentTypeObj)
	}
	return contentType, nil
}
