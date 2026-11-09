# Experimental Pkl authoring fixtures

These files are **not the released Dill schema** and their JSON is **not accepted by current Dill**. Do not run `dill up` against them. They demonstrate idiomatic Pkl authoring for the [roadmap](../../../ROADMAP.md), not a complete implementation contract. Issue [#38](https://github.com/umayr/dill/issues/38) owns the eventual schema/IR design.

## Files

- `Model.pkl`: typed named listings for declarations/bindings, naturally keyed environment mappings, disjoint network/storage forms, and explicit Duration/DataSize JSON conversion. The renderer is a `JsonRenderer` value: the standard class is not open for subclassing.
- `Postgres.pkl`: reusable `Component` class exporting a `Model.Service`, with caller-selected storage/secret identities. Lexically captured local properties avoid incorrect nested `outer` references.
- `composed.pkl`: two component instances; one instance amended for memory; manual job; existing storage and secret bindings.
- `downloads.pkl`: shared VPN namespace requirement, typed temporary storage and trusted-context references using synthetic host paths.

Run:

```sh
PKL=/path/to/pkl-0.31.1 python3 docs/design/check_examples.py
pkl eval docs/design/examples/composed.pkl
pkl eval docs/design/examples/downloads.pkl
```

The helper writes negative fixtures only to a temporary directory. It checks evaluation, named-list rendering, composition/override isolation, explicit unit encoding, repeatable values and invalid scalar constraints. A fixture-only index helper checks order-independent lookup and rejects duplicate names: this is a test of the intended normalization rule, not an implemented Dill semantic validator. Duplicate names can still pass Pkl shape evaluation; the future runtime must reject them before creating internal maps, including service/job namespace collisions. It does **not** assert that a secret exists, a referenced node exists, a graph is acyclic, host paths are accessible, operator policy allows requests, or a runtime can execute the model. Those are separate acceptance tests in the linked tickets.

`imageHealthcheck` requires explicit image metadata resolution and runtime observation in the future implementation. `command` readiness is a declaration, not an executed command here. The initializer's `exit 0` is a harmless example, not a real migration. The download fixture intentionally omits application-specific production details; it is not a complete hardened VPN recipe. Never infer VPN traffic safety from successful Pkl evaluation.

The prototypes import a shared local schema. Published-package composition, minimum-version selection, checksum failures and incompatible schema majors still require the independent-publisher conformance tests in [#45](https://github.com/umayr/dill/issues/45). No fictitious package URI or untested `@NEXT` release is presented as usable.

Public declarations use `Listing` objects with an explicit `name`, for example `secrets { new { name = "wireguard" } }`. For existing volumes, `name` identifies the logical binding and `volume` is the actual engine volume name. Amend a component's exported object before insertion, as demonstrated by `catalogService`; do not patch list indexes. Environment values remain mappings. Listing order is not lifecycle order: the DAG determines startup, and declaration-only reorderings must not change semantic hashes.

When adapting syntax, use classes, typed named listings, naturally keyed mappings and object amendment; do not transcribe examples into snake_case Compose-shaped fields. Before implementing new variants, settle normalization, cross-reference validation, graph implications, hash/diff behavior, target capabilities and authorization. A syntactically accepted field is not a supported feature.
