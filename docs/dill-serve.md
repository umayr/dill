# Experimental local status service

`make build-serve` builds the optional `bin/dill-serve` separately; it is not included in the default `dill` build. Place the binary on your `PATH` or invoke it directly. This does not replace standalone `dill`.

By default the service provides health and status only. Optional snapshot staging is described below; there is no deployment or secret delivery. Configure one engine endpoint and one stack at startup:

```sh
mkdir -m 700 /absolute/private/dill-service
bin/dill-serve --engine docker --endpoint unix:///absolute/path/to/docker.sock \
  --stack my-stack --socket /absolute/private/dill-service/api.sock
curl --unix-socket /absolute/private/dill-service/api.sock http://localhost/v1/health
curl --unix-socket /absolute/private/dill-service/api.sock http://localhost/v1/status
```

`--engine` is `docker` or `podman`; the endpoint must be an explicit local Unix URL. There is no environment-based endpoint selection, TCP listener, request-selected stack, socket or path, or CLI subprocess. Control characters are rejected in the endpoint and service socket path. The socket directory must already exist, be owned by the effective user, and have mode 0700; symlinked directory components and existing socket paths (including symlinks) are rejected. The service creates a mode 0600 socket and removes only its own socket on normal shutdown. A crash can leave a stale socket; the operator must verify and remove it before restarting.

See [snapshot staging](snapshot-staging.md) for the optional upload contract, trust boundary, and retention limitations. Trusted project snapshots may contain secrets; the sender must warn and obtain confirmation before upload. This is not secret delivery.

## HTTP v1 contract

GET requests must not have a body or query parameters. Responses are JSON:

* `/v1/health`: `{"api_version":"v1","capabilities":["health","status"]}`. This indicates the process is serving, not that the engine is healthy.
* `/v1/status`: a JSON object containing `stack`, `engine`, and `containers` from shared `core.Status` for the fixed target. No configuration is loaded, so configuration file and service count are omitted rather than reported as known. Individual container inspection failures appear as error-state containers with the fixed status `container status unavailable`; raw inspection errors are not returned.

Errors have `code` and `message`: `invalid_request` (400), `method_not_allowed` (405), `unknown_operation` (404), `engine_error` (502), `timeout` (504), or `response_too_large` (500). Header/read/write/idle and status-operation timeouts apply; JSON responses are capped at 1 MiB. HTTP parser-level errors may not use this JSON envelope. The status operation calls the engine and may reveal container names, image names, ports, and container states.

The operator is responsible for authorizing access to the Unix socket and protecting the engine endpoint. Any process able to access the socket has the same read access; there is no application authentication or per-client isolation. The private directory and its ancestors must not be writable by untrusted users; same-user processes are trusted. Resource bounds are not a strict memory or concurrency quota: engine result construction can exceed the response limit before serialization. No deployment, plan, configuration evaluation, or remote strict-secret capability is provided. API v1 is experimental and intended as a narrow integration point for a future Brine client.
