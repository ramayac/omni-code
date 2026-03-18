.PHONY: build test dev docker-db clean

# Run ChromaDB locally for development
docker-db:
	docker run -d -p 8000:8000 --name chroma-db chromadb/chroma

# Build the binary
build:
	go build -o bin/omni-code ./cmd/omni-code

# Run tests
test:
	go test -v ./...

# Run in dev mode (index the test-data repo)
dev: build
	./bin/omni-code index --name test-repo ./test-data

# Clean build artifacts
clean:
	rm -rf bin/
