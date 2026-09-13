package main

import "os"

func readFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	return string(content), err
}
