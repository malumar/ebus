.PHONY: test test-race bench bench-long vet lint ci

GO      ?= go
GOBIN   := $(shell $(GO) env GOBIN)
ifeq ($(GOBIN),)
GOBIN   := $(shell $(GO) env GOPATH)/bin
endif
STATICCHECK := $(GOBIN)/staticcheck


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
	@command -v $(STATICCHECK) >/dev/null 2>&1 || { \
		echo "Installing staticcheck..."; \
		GOTOOLCHAIN=local CGO_ENABLED=0 $(GO) install honnef.co/go/tools/cmd/staticcheck@2024.1.1; \
	}
	$(STATICCHECK) -checks=all,-ST1006 ./...
ci: test-race vet lint