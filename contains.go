package ybro

import (
	"fmt"
	"strings"
)

func ErrContains(inpErr error, sub string) bool {
	errS := fmt.Sprintf("%+v", inpErr)
	errSLower := strings.ToLower(errS)
	subLower := strings.ToLower(sub)
	return strings.Contains(errSLower, subLower)
}

func IsDuplicateInsertKeyErr(inpErr error) bool {
	return ErrContains(inpErr, "duplicate key value violates unique constraint")
}
