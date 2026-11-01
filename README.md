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
  up        Start all services (default)
  down      Stop and remove all dill-managed services
  teardown  Remove containers, images, volumes, and network
  stop      Stop services without removing containers
  start     Start previously stopped containers
  restart   Restart services
  ps        List containers in the stack
  status    Show stack status
  logs      Fetch container logs
  pull      Pull service images without starting
  images    List images used by the config
  volumes   List stack-owned volumes
  exec      Execute a command in a running container
  plan      Show what would change if you ran up
  diff      Show desired vs live changes
  stacks    List dill-managed stacks (alias: ls)
  doctor    Check local dill, Pkl, and engine readiness
  rm        Remove stopped containers in the stack
  wait      Wait for services to become ready
  port      Print published ports for a service
  top       Show processes in a running container
  cp        Copy files between a container and the host
  config    Print the resolved config
  init      Create a starter compose.pkl
  validate  Check a .pkl config file for errors
  fmt       Format a .pkl config file in-place
  version   Print the dill version

Flags:
  -f <path>         Path to .pkl config file (default: compose.pkl)
  -p, --stack-name  Override the stack name (also --project-name)
  --engine          Override the config engine (podman or docker)
  --timeout <dur>   Timeout for the stack to become ready (default: 5m)
  --env-file        Load KEY=VALUE pairs before evaluating config
  --force-recreate  Recreate matching containers during up
  --dry-run         Show intended changes without mutating containers
  --quiet           Suppress progress output
  -y, --yes         Assume yes for destructive prompts
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

To inspect and operate on a stack:
```
./dill -f examples/fullstack/compose.pkl status
./dill -f examples/fullstack/compose.pkl plan --format json
./dill -f examples/fullstack/compose.pkl logs --follow --tail 100 web
./dill -f examples/fullstack/compose.pkl wait web
./dill -f examples/fullstack/compose.pkl exec web sh
```

For machine-readable output, `ps`, `status`, `plan`/`diff`, `stacks`,
`doctor`, `images`, `config`, `validate`, and `port` support `--format` where applicable.
Lifecycle commands accept service names as positional arguments, for example
`dill up web`, `dill restart web`, or `dill down web`. Use `--no-deps` with
`up`, `start`, or `restart` when you intentionally do not want dependency services
included.

`plan` is CI-oriented: it exits non-zero when changes are detected. `diff`
renders the same desired-vs-live information but exits zero unless the command
itself fails. `rm` and `teardown` prompt before destructive removal; pass
`--yes`/`-y` for automation. `stop` and `down` do not prompt.

#### Notes

- `logs` uses `--follow` (short form `-n`/`--tail` still apply). `-f` is
  reserved everywhere for the config-file path, never for following logs.
- `stacks` and `doctor` can run without a config file. `stacks` therefore has
  no config to derive an engine from and defaults to Podman; pass
  `--engine docker` to list Docker stacks.

#### Exit codes

| Code | Meaning |
|------|---------|
| `0`  | Success (including `diff` with drift and `doctor` with no issues) |
| `1`  | Operational failure, `plan`/`doctor` detected drift or issues |
| `2`  | Usage error (unknown command, bad flag, unknown service, invalid format) |

## Integration Tests

Dill has opt-in integration tests that exercise the real Docker and Podman APIs:

```
DILL_TEST_ENGINE=docker make integration-test
DILL_TEST_ENGINE=podman PODMAN_SOCKET=unix:///tmp/podman.sock make integration-test
```

Useful integration-test knobs:

```
DILL_BINARY=/path/to/dill       # reuse an existing binary
DILL_TEST_RUN=TestDillCommandUX # pass a -run filter through make
DILL_TEST_TIMEOUT=2m            # default command timeout for fixtures
DILL_TEST_KEEP=1                # keep failed stacks for debugging
```

On macOS, the local helper scripts create VM-backed engines:

```
scripts/integration-colima-docker.sh
scripts/integration-lima-podman.sh
scripts/integration-local.sh
```

The Docker script uses a Colima VM. The Podman script uses a Lima Ubuntu VM,
installs Go/Pkl/Podman if missing, starts a per-user Podman API socket inside
the VM, and runs the same Go integration tests there. On failure, the scripts
dump engine state (`version`, `info`, containers, images, volumes, and networks)
to make cleanup and debugging less guessy.

CI runs the same integration suite against Docker and Podman on GitHub Actions.

## Configuration Schema

Dill configs are `.pkl` files that amend the published `dill` schema:

```pkl
amends "package://pkg.ushd.ca/dill@0.4.0#/dill.pkl"

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

`pull_policy` is applied before planning: `always` pulls before the plan is computed,
and Dill recreates a container when the local image ID differs from the image it is
running (for example after `dill pull` or a moved tag). `missing` only pulls when the
image is absent; `never` uses the local image as-is.

### Volumes

Named volumes are stack-scoped by default: `pg-data:/var/lib/postgresql/data`
creates `<stack>_pg-data`. Two stacks declaring the same logical volume get two
independent volumes, and `dill teardown` only removes volumes labelled as owned by
the stack.

To reuse a pre-existing volume (for example one Compose created as
`<project>_pg-data`) or a shared volume, use the full mount form:

```pkl
volumes = List(new Mount {
  type = "volume"
  source = "pg-data"
  target = "/var/lib/postgresql/data"
  external_volume = true
})
```

`external_volume = true` uses `source` (or `name`) as the engine volume name and
Dill never creates, labels, or removes it. A `name` without `external_volume` uses
an explicit engine name but keeps the volume managed.

`dill volumes` lists the volumes owned by the stack. Legacy and Compose-created
volumes are unlabelled and are left untouched by teardown. If the stack name matches
the Compose project name, the default `<stack>_<volume>` already points at the
existing volume.

## Why Dill?
- **Type Safety:** Catch invalid ports or misspelled parameters in your IDE.
- **Programmability:** Use loops, conditionals, and variables to build complex architectures.
- **Engine Agnostic:** Deploy securely with Podman's daemonless architecture, or fallback to Docker when needed.
- **DAG Startup:** Services with `depends_on` start in order and only after their dependencies are healthy.
- **Image-aware updates:** `pull_policy: always` pulls before planning and recreates only when the image ID actually changed.
