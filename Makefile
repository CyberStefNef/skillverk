.PHONY: build install test check preview

build:
	go build -trimpath -o bin/skillverk .

install: build
	mkdir -p "$(HOME)/.local/bin"
	install -m755 bin/skillverk "$(HOME)/.local/bin/skillverk"

test:
	go test ./...

check: test
	go vet ./...

preview:
	SKILLVERK_RENDER_PATH="$(CURDIR)/docs/preview.ansi" go test ./internal/tui -run TestRenderDocumentation -count=1
	node scripts/render-preview.mjs
