# Dill

Dill is a strictly-typed, configuration-as-code orchestrator built with Pkl and Go. It serves as a modern alternative to Docker Compose, catching validation errors before runtime and providing native support for both Podman and Docker.

## Setup

1. **Install Pkl:** Follow the official instructions at `pkl-lang.org`.
2. **Install Go dependencies:**
   `go mod tidy`
3. **Build the binary:**
   `go build -tags containers_image_openpgp -o dill ./cmd/dill`

> The `-tags containers_image_openpgp` flag uses a pure-Go OpenPGP implementation,
> avoiding a C dependency on `libgpgme`. Omit it only if you have `pkg-config` and
> `gpgme` installed (e.g. `brew install pkg-config gpgme` on macOS).

## Usage

By default, Dill uses the Podman socket. You can switch to Docker by setting `engine = "docker"` in your configuration.

```
dill [flags] [command]

Commands:
  up    Start all services (default)
  down  Stop and remove all dill-managed services

Flags:
  -f <path>         Path to .pkl config file (default: compose.pkl)
  --timeout <dur>   Startup timeout per service (default: 60s)
  -v / -vv / -vvv / -vvvv   Verbosity (info / debug / debug+caller / +timestamps)
  -v=N              Numeric verbosity
```

To deploy a stack:
```
DB_USER=admin DB_PASSWORD=secret ./dill -v -f examples/overrides/my-server.pkl up
```

To tear it down:
```
./dill -f examples/overrides/my-server.pkl down
```

## Configuration Schema

Dill configs are `.pkl` files that amend the published `dill` schema:

```pkl
amends "package://github.com/umayr/dill@0.0.6#/dill.pkl"

config {
  engine = "docker"
  services {
    ["web"] {
      image = "nginx:alpine"
      ports = List("8080:80")
      pull_policy = "missing"   // "always" | "missing" | "never"
    }
  }
}
```

## Why Dill?
- **Type Safety:** Catch invalid ports or misspelled parameters in your IDE.
- **Programmability:** Use loops, conditionals, and variables to build complex architectures.
- **Engine Agnostic:** Deploy securely with Podman's daemonless architecture, or fallback to Docker when needed.
- **DAG Startup:** Services with `depends_on` start in order and only after their dependencies are healthy.
