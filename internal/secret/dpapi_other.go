//go:build !windows

package secret

import "errors"

var errUnsupported = errors.New("secret: DPAPI is only available on Windows")

func Protect(plain []byte) ([]byte, error) {
	return nil, errUnsupported
}

func Unprotect(cipher []byte) ([]byte, error) {
	return nil, errUnsupported
}
