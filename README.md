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

If Dill cannot find `pkl` on `PATH`, it can download the pinned Pkl release, but
it now requires checksum verification. Prefer installing Pkl yourself or setting
`DILL_PKL_PATH`. For automated installs, set `DILL_PKL_SHA256` to the expected
binary hash. `DILL_ALLOW_UNVERIFIED_PKL_DOWNLOAD=1` exists only as an explicit
escape hatch for disposable environments.

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

## Integration Tests

Dill has opt-in integration tests that exercise the real Docker and Podman APIs:

```
DILL_TEST_ENGINE=docker make integration-test
DILL_TEST_ENGINE=podman PODMAN_SOCKET=unix:///tmp/podman.sock make integration-test
```

On macOS, the local helper scripts create VM-backed engines:

```
scripts/integration-colima-docker.sh
scripts/integration-lima-podman.sh
scripts/integration-local.sh
```

The Docker script uses a Colima VM. The Podman script uses a Lima Ubuntu VM,
installs Go/Pkl/Podman if missing, starts the Podman API socket inside the VM,
and runs the same Go integration tests there.

CI runs the same integration suite against Docker and Podman on GitHub Actions.

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
