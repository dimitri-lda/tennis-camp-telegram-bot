# Project guidelines

- Use Go 1.27.1.
- Keep the architecture simple and introduce components only when they are needed.
- Use the standard `net/http` package for HTTP functionality.
- Use polling for local development and a webhook in production.
- Read configuration from environment variables.
- Do not introduce Docker, Kubernetes, Redis, queues, or a database at this stage.
- Never log tokens or personal data.
- After changes, run `go fmt ./...`, `go vet ./...`, and `go test ./...`.
