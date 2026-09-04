//go:build !windows

package main

// attachParentConsole has nothing to do where the application is linked with a
// console in the first place.
func attachParentConsole() {}
