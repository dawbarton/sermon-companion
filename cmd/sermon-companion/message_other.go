//go:build !windows

package main

// showFatalMessage has nothing to add where the application was started from a
// terminal: the message has already been written to standard error.
func showFatalMessage(string) {}
