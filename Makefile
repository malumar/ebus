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
	GOTOOLCHAIN=local CGO_ENABLED=0 $(GO) run honnef.co/go/tools/cmd/staticcheck@2024.1.1 \
	  -checks=all,-ST1006 ./...
ci: test-race vet lint