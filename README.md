# Dill

Dill is a strictly-typed, configuration-as-code orchestrator built with Pkl and Go. It serves as a modern alternative to Docker Compose, catching validation errors before runtime and providing native support for both Podman and Docker.

## Setup

1. **Install Pkl:** Follow the official instructions at `pkl-lang.org`.
2. **Install Go dependencies:**
   `go mod tidy`
3. **Build the binary:**
   `go build -o dill ./cmd/dill`

## Usage

By default, Dill uses the Podman socket. You can switch to Docker by setting `engine = "docker"` in your configuration.

To deploy a stack:
`DB_USER=admin DB_PASSWORD=secret ./dill -f examples/overrides/my-server.pkl`

## Why Dill?
- **Type Safety:** Catch invalid ports or misspelled parameters in your IDE.
- **Programmability:** Use loops, conditionals, and variables to build complex architectures.
- **Engine Agnostic:** Deploy securely with Podman's daemonless architecture, or fallback to Docker when needed.