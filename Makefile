.PHONY: build test test-short ingest demo

build:
	go build -o bin/memeshorts ./cmd/memeshorts

test:
	go test ./...

# bez ffmpeg end-to-end testu
test-short:
	go test -short ./...

ingest: build
	./bin/memeshorts ingest

demo: build
	./bin/memeshorts run --meme imgflip:181913649 --recipe kenburns \
		--text "Writing unit tests" --text "Testing in production"
