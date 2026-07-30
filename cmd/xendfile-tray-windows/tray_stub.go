//go:build !windows

package main

import "errors"

func runTray(_ string, _ bool) error {
	return errors.New("the Windows tray companion can only run on Windows")
}
