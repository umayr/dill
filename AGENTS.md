# Dill

- Dill owns the Pkl schema, CLI, plan, DAG, engine adapters and target-side evidence. Brine owns the browser, workspaces, Git automation and operator workflow. Change the contract here; verify consumers separately.
- Validate before mutation. Check ownership, preserve data on uncertainty, and distinguish failed, partial and unknown outcomes. Never bypass secret-history checks or restore retired Alpha execution as a fallback.
- Keep Docker and Podman behavior aligned. Schema changes need Pkl validation, Go decoding and normalization, plan/drift handling, both adapters where relevant, and tests.
- Keep supported behavior and recovery instructions in the repo. Remove obsolete paths and proposals rather than archiving them here. Never modify published schema versions or release assets.
- Use a branch in a new worktree under `.worktree/`. Run `make build test vet validate` with the pinned Pkl version; use `make integration-test` against disposable engines for runtime changes. Plain Go builds omit required tags.
