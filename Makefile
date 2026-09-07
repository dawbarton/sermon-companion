.PHONY: test run-demo build windows

test:
	go test ./...
	node --test internal/app/testdata/common_test.js

run-demo:
	go run ./cmd/sermon-companion --demo --data-dir ./work/demo-data

build:
	go build ./cmd/sermon-companion

windows:
	pwsh -File scripts/build-windows.ps1
