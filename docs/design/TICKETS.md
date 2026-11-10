# Implementation tickets

This is the coordinated backlog for the [roadmap](../../ROADMAP.md). Issues describe planned work, not shipped features. Dependency links are sequencing constraints; phases are approximate and do not authorize bypassing dependencies. Existing Dill issues were revised rather than duplicated.

## #19 staged dependency gate

The implemented experimental foundation provides metadata-only protocol
discovery/check/codec behavior. Its **`protocol-foundation-1`** gate is defined
in the [protocol foundation contract](protocol-contract-19-proposal.md) and
passes after independent review, passing full final-head CI and merge to main.

For downstream **contract work**, the #19 prerequisite links below may be
satisfied by that named foundation gate rather than full #19 closure. This
allows #38/#16 and subsequent #10 contracts to establish reference-safe IR,
semantic hashes and late secret resolution without waiting for #19 to provide
those dependent features first. All other prerequisites remain in force.

Foundation passage does not authorize artifact export/apply, target/provider
access, managed mutation or durable control. Each runtime capability still needs
its own safety contracts and verification. **#19 remains OPEN** until full
secret-safe artifacts, Pkl-free semantic validation/application, target checks,
runtime outcomes and Brine integration acceptance are met. In particular,
Brine's managed mutations still require #47/#42 and the applicable runtime
capabilities; protocol codec fixtures alone are not integration evidence.

## This repository

| Ticket | Phase | Prerequisites |
|---|---|---|
| [Define typed application/deployment contracts and canonical semantic IR](https://github.com/umayr/dill/issues/38) | 1 — contracts | [dill#17](https://github.com/umayr/dill/issues/17), [dill#19](https://github.com/umayr/dill/issues/19) |
| [Unify offline semantic validation and failure exit contracts](https://github.com/umayr/dill/issues/32) | 0 — correctness | — |
| [Propagate partial lifecycle and cleanup failures with structured results](https://github.com/umayr/dill/issues/31) | 0 — correctness | — |
| [Pin Pkl and publish immutable, verifiable schema releases](https://github.com/umayr/dill/issues/17) | 0 — correctness | — |
| [Version rendered artifacts and the Dill machine-operation protocol](https://github.com/umayr/dill/issues/19) | 1 — contracts | [dill#32](https://github.com/umayr/dill/issues/32), [dill#31](https://github.com/umayr/dill/issues/31) |
| [Version semantic hashes and secret-safe change detection](https://github.com/umayr/dill/issues/16) | 1 — contracts | [dill#19](https://github.com/umayr/dill/issues/19) |
| [Model dependency edges, bounded parallel execution and reverse lifecycle ordering](https://github.com/umayr/dill/issues/11) | 1 — contracts | [dill#32](https://github.com/umayr/dill/issues/32), [dill#38](https://github.com/umayr/dill/issues/38) |
| [Add first-class jobs, explicit readiness and durable completion semantics](https://github.com/umayr/dill/issues/24) | 2 — application relationships | [dill#11](https://github.com/umayr/dill/issues/11), [dill#42](https://github.com/umayr/dill/issues/42), [dill#19](https://github.com/umayr/dill/issues/19) |
| [Implement typed network attachments and namespace lifecycle coupling](https://github.com/umayr/dill/issues/9) | 2 — application relationships | [dill#11](https://github.com/umayr/dill/issues/11), [dill#40](https://github.com/umayr/dill/issues/40) |
| [Introduce typed storage bindings and target-side storage preconditions](https://github.com/umayr/dill/issues/39) | 2 — data safety | [dill#38](https://github.com/umayr/dill/issues/38), [dill#40](https://github.com/umayr/dill/issues/40), [dill#41](https://github.com/umayr/dill/issues/41) |
| [Add late-bound secret references and isolated deployment environment bindings](https://github.com/umayr/dill/issues/10) | 1 — contracts | [dill#38](https://github.com/umayr/dill/issues/38), [dill#16](https://github.com/umayr/dill/issues/16), [dill#19](https://github.com/umayr/dill/issues/19) |
| [Add typed process, logging, device and security requirements](https://github.com/umayr/dill/issues/12) | 2 — application relationships | [dill#38](https://github.com/umayr/dill/issues/38), [dill#40](https://github.com/umayr/dill/issues/40), [dill#41](https://github.com/umayr/dill/issues/41) |
| [Model restart policy and bounded retry counts idiomatically](https://github.com/umayr/dill/issues/25) | 2 — application relationships | [dill#38](https://github.com/umayr/dill/issues/38) |
| [Audit existing resource limits and evolve toward typed quantities](https://github.com/umayr/dill/issues/8) | 0/2 — audit then schema evolution | [dill#15](https://github.com/umayr/dill/issues/15) |
| [Implement staged capability-aware preflight and apply-time revalidation](https://github.com/umayr/dill/issues/40) | 1 — contracts | [dill#32](https://github.com/umayr/dill/issues/32), [dill#19](https://github.com/umayr/dill/issues/19) |
| [Define operator-authoritative safety policy and explicit deletion plans](https://github.com/umayr/dill/issues/41) | 1 — contracts | [dill#19](https://github.com/umayr/dill/issues/19) |
| [Add cross-process stack operations, identity locking and durable outcomes](https://github.com/umayr/dill/issues/42) | 1 — contracts | [dill#19](https://github.com/umayr/dill/issues/19), [dill#16](https://github.com/umayr/dill/issues/16) |
| [Add prepared deployment candidates, safe updates and recovery records](https://github.com/umayr/dill/issues/43) | 3 — operational confidence | [dill#42](https://github.com/umayr/dill/issues/42), [dill#40](https://github.com/umayr/dill/issues/40), [dill#41](https://github.com/umayr/dill/issues/41), [dill#24](https://github.com/umayr/dill/issues/24) |
| [Integrate application-consistent backup and isolated restore verification](https://github.com/umayr/dill/issues/44) | 3 — operational confidence | [dill#39](https://github.com/umayr/dill/issues/39), [dill#24](https://github.com/umayr/dill/issues/24), [dill#46](https://github.com/umayr/dill/issues/46), [dill#42](https://github.com/umayr/dill/issues/42) |
| [Specify open Pkl recipe packages, static catalogs and explicit dependency workflows](https://github.com/umayr/dill/issues/45) | 2/3 — reuse after contracts | [dill#38](https://github.com/umayr/dill/issues/38), [dill#17](https://github.com/umayr/dill/issues/17), [dill#19](https://github.com/umayr/dill/issues/19), [dill#10](https://github.com/umayr/dill/issues/10) |
| [Define narrow trusted provider contracts; defer general plugin marketplace](https://github.com/umayr/dill/issues/46) | 3 — integrations | [dill#19](https://github.com/umayr/dill/issues/19), [dill#41](https://github.com/umayr/dill/issues/41), [dill#10](https://github.com/umayr/dill/issues/10) |
| [Define trusted target contexts and explicit configuration precedence](https://github.com/umayr/dill/issues/47) | 1/2 — target identity | [dill#19](https://github.com/umayr/dill/issues/19), [dill#40](https://github.com/umayr/dill/issues/40), [dill#41](https://github.com/umayr/dill/issues/41) |
| [Import Compose intent and provide a safe Portainer cutover report](https://github.com/umayr/dill/issues/7) | 2 — migration tooling | [dill#38](https://github.com/umayr/dill/issues/38), [dill#9](https://github.com/umayr/dill/issues/9), [dill#39](https://github.com/umayr/dill/issues/39), [dill#24](https://github.com/umayr/dill/issues/24), [dill#10](https://github.com/umayr/dill/issues/10), [dill#41](https://github.com/umayr/dill/issues/41) |
| [Enforce runtime capability contracts and normalized Docker/Podman behavior](https://github.com/umayr/dill/issues/15) | 0/ongoing — implementation discipline | — |
| [Build a homelab-oriented conformance and failure-path test matrix](https://github.com/umayr/dill/issues/18) | 0/ongoing — evidence | [dill#15](https://github.com/umayr/dill/issues/15) |
| [Support private image authentication and explicit image preparation](https://github.com/umayr/dill/issues/13) | 2 — artifacts | [dill#40](https://github.com/umayr/dill/issues/40), [dill#10](https://github.com/umayr/dill/issues/10) |
| [Add explicit selection profiles and safe replica expansion after core contracts](https://github.com/umayr/dill/issues/14) | 4 — optional scale | [dill#11](https://github.com/umayr/dill/issues/11), [dill#41](https://github.com/umayr/dill/issues/41), [dill#39](https://github.com/umayr/dill/issues/39) |
| [Explore Apple container and Incus application backends with capability gates](https://github.com/umayr/dill/issues/48) | 4 — experimental backends | [dill#15](https://github.com/umayr/dill/issues/15), [dill#47](https://github.com/umayr/dill/issues/47), [dill#40](https://github.com/umayr/dill/issues/40) |
| [Design systemd and Quadlet boot integration without competing controllers](https://github.com/umayr/dill/issues/49) | 4 — host integration | [dill#47](https://github.com/umayr/dill/issues/47), [dill#42](https://github.com/umayr/dill/issues/42), [dill#39](https://github.com/umayr/dill/issues/39) |

## Companion repository

| Ticket | Phase | Prerequisites |
|---|---|---|
| [Negotiate the Dill operation protocol and make managed mutations single-path](https://github.com/umayr/brine/issues/5) | 0/1 — integration foundation | [dill#19](https://github.com/umayr/dill/issues/19), [dill#47](https://github.com/umayr/dill/issues/47), [dill#42](https://github.com/umayr/dill/issues/42) |
| [Persist deployment jobs, candidate approvals and recovery-aware history](https://github.com/umayr/brine/issues/6) | 1/3 — operation workflow | [brine#5](https://github.com/umayr/brine/issues/5), [dill#43](https://github.com/umayr/dill/issues/43), [dill#42](https://github.com/umayr/dill/issues/42) |
| [Implement authoritative Git/managed workspaces and full Pkl project files](https://github.com/umayr/brine/issues/7) | 1 — configuration management | [brine#5](https://github.com/umayr/brine/issues/5), [dill#38](https://github.com/umayr/dill/issues/38), [dill#45](https://github.com/umayr/dill/issues/45) |
| [Add per-deployment binding workflows without turning Brine into a vault](https://github.com/umayr/brine/issues/8) | 1 — secret/environment isolation | [brine#5](https://github.com/umayr/brine/issues/5), [brine#7](https://github.com/umayr/brine/issues/7), [dill#10](https://github.com/umayr/dill/issues/10) |
| [Add secure multi-host execution and inventory without a scheduler](https://github.com/umayr/brine/issues/9) | 2 — homelab targets | [brine#5](https://github.com/umayr/brine/issues/5), [dill#47](https://github.com/umayr/dill/issues/47), [dill#42](https://github.com/umayr/dill/issues/42), [brine#12](https://github.com/umayr/brine/issues/12) |
| [Build source-aware recipe discovery and typed deployment forms](https://github.com/umayr/brine/issues/10) | 2/3 — recipe UX | [brine#7](https://github.com/umayr/brine/issues/7), [brine#8](https://github.com/umayr/brine/issues/8), [dill#45](https://github.com/umayr/dill/issues/45) |
| [Add bounded control-plane integrations for events and schedules](https://github.com/umayr/brine/issues/11) | 3 — integrations | [brine#6](https://github.com/umayr/brine/issues/6), [brine#12](https://github.com/umayr/brine/issues/12), [dill#44](https://github.com/umayr/dill/issues/44) |
| [Harden control-plane authorization, credentials and audit boundaries](https://github.com/umayr/brine/issues/12) | 1/2 — operator security | [brine#5](https://github.com/umayr/brine/issues/5) |
| [Support staged Portainer coexistence and homelab cutover evidence](https://github.com/umayr/brine/issues/13) | 2 — adoption | [brine#9](https://github.com/umayr/brine/issues/9), [brine#8](https://github.com/umayr/brine/issues/8), [brine#6](https://github.com/umayr/brine/issues/6), [dill#7](https://github.com/umayr/dill/issues/7) |
| [Expose actionable preflight, drift and recovery diagnostics](https://github.com/umayr/brine/issues/14) | 2/3 — operator confidence | [brine#5](https://github.com/umayr/brine/issues/5), [brine#6](https://github.com/umayr/brine/issues/6), [dill#40](https://github.com/umayr/dill/issues/40), [dill#43](https://github.com/umayr/dill/issues/43) |
