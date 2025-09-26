.PHONY: test test-race bench bench-long vet lint ci

GO ?= go

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

bench:
	$(GO) test -bench=. -benchmem ./...

bench-long:
	$(GO) test -bench=. -benchmem -benchtime=2s -count=5 ./...

vet:
	$(GO) vet ./...

lint:
	@which staticcheck >/dev/null 2>&1 || { echo "Installing staticcheck..."; $(GO) install honnef.co/go/tools/cmd/staticcheck@latest; }
	staticcheck ./...

ci: test-race vet lint