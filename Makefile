.PHONY: test run-demo build windows

test:
	go test ./...

run-demo:
	go run ./cmd/sermon-companion --demo --data-dir ./work/demo-data

build:
	go build ./cmd/sermon-companion

windows:
	pwsh -File scripts/build-windows.ps1
