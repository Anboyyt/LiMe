//go:build !linux

package api

import "errors"

func diskUsage(path string) (free, total uint64, err error) {
	return 0, 0, errors.New("disk usage unsupported on this platform")
}
