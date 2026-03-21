.PHONY: help build test dev docker-db docker-db-start stop-db rm-db clean fmt vet tidy \
	reset-db reset-repo reindex reindex-repo estimate backup-db restore-db status \
	init-config query chat

# Show this help message
help:
	@awk '/^# /{c=substr($$0,3);next}c&&/^[[:alpha:]][[:alnum:]_-]+:/{printf "  \033[36m%-18s\033[0m %s\n", $$1, c}1{c=0}' $(MAKEFILE_LIST)

# Run ChromaDB locally for development
docker-db:
	docker run -d -p 8000:8000 --name chroma-db chromadb/chroma

# Start existing container or create it if missing (idempotent)
docker-db-start:
	@docker start chroma-db 2>/dev/null || docker run -d -p 8000:8000 --name chroma-db chromadb/chroma

# Stop the running ChromaDB container
stop-db:
	docker stop chroma-db

# Remove the ChromaDB container (run backup-db first!)
rm-db:
	docker rm chroma-db

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

# Drop all ChromaDB collections and recreate them
reset-db: build
	./bin/omni-code reset --all

# Reset a single repo's data; usage: make reset-repo REPO=myapp
reset-repo: build
ifndef REPO
	$(error REPO is not set. Usage: make reset-repo REPO=myapp)
endif
	./bin/omni-code reset --repo $(REPO)

# Reindex all repos from repos.yaml
reindex: build
	./bin/omni-code index --config repos.yaml

# Reindex a single repo from repos.yaml; usage: make reindex-repo REPO=myapp
reindex-repo: build
ifndef REPO
	$(error REPO is not set. Usage: make reindex-repo REPO=myapp)
endif
	./bin/omni-code index --config repos.yaml --repo $(REPO)

# Backup ChromaDB data to ./backups/<timestamp>/
backup-db:
	@ts=$$(date +%Y-%m-%dT%H-%M-%S) && \
	mkdir -p backups/$$ts && \
	docker exec chroma-db tar -czf - /chroma/chroma > backups/$$ts/chroma.tar.gz && \
	echo "[backup] saved to backups/$$ts/chroma.tar.gz"

# Restore ChromaDB from a backup archive; usage: make restore-db FILE=./backups/.../chroma.tar.gz
restore-db:
ifndef FILE
	$(error FILE is not set. Usage: make restore-db FILE=./backups/2026-03-17/chroma.tar.gz)
endif
	docker cp $(FILE) chroma-db:/tmp/restore.tar.gz
	docker exec chroma-db tar -xzf /tmp/restore.tar.gz -C /
	@echo "[restore] done from $(FILE)"

# Print repo index status table (REPO, BRANCH, COMMIT, LAST INDEXED, FILES, CHUNKS, MODE, DURATION)
status: build
	./bin/omni-code repos --config repos.yaml

# Format source code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Tidy module dependencies
tidy:
	go mod tidy

# Estimate and print sorted scan complexity without indexing
estimate: build
	./bin/omni-code index --config repos.yaml --dry-run

# Create repos.yaml from example template (refuses if file already exists)
init-config:
	@if [ -f repos.yaml ]; then \
		echo "\033[33m[warn]\033[0m repos.yaml already exists — not overwriting."; \
		echo "       Edit it directly or remove it first."; \
	else \
		cp repos-example.yaml repos.yaml; \
		echo "[init] created repos.yaml from repos-example.yaml — edit it with your repos."; \
	fi

# Run a search query; usage: make query Q="how does change detection work"
query: build
ifndef Q
	$(error Q is not set. Usage: make query Q="how does change detection work")
endif
	./bin/omni-code search --query "$(Q)" --hybrid

# Start interactive chat session (OpenAI-compatible endpoint)
chat: build
	./bin/omni-code chat --config repos.yaml

# Start MCP locally
mcp: build
	./bin/omni-code mcp --transport sse --addr :8090
