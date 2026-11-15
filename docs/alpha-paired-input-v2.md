# Explicit v2 Dill input for a future paired Alpha qualification

This is an **offline accepted-input verifier**, not an application driver,
installer, downloader, builder, source approval, host attestation or new native
qualification. It does not start Brine, Dill, Pkl, an engine or a subprocess.
The original Pkl intrinsic-authority audit is **deferred, not waived**; source
execution and actual paired application qualification remain disabled.

## One accepted main input, explicit version

Use `scripts/prepare-alpha-pair-v2.py` only for the source-owned twenty-key pin
`test/qualification/pair/v2/dill-input.json`:

- Dill main `2d48de89947c1bec0c4e305b6d05e9172cfdaecd`, tree
  `5a4b96b8410be7f852bfcb09f80ddb48469c72e2`.
- Exact-main CI `43666038516`; qualification `43666204662`.
- Docker artifact `14143195067`, 79915790 bytes, ZIP SHA256
  `3cbf172ed1b5e802ebe615bba38a9022ee9b17849824357a21831df0386b43bc`.
- Absolute official expiry **2026-11-29T02:16:09Z**.
- Actual Linux amd64 Dill SHA256
  `5338c665f0ca710dd6fbc3f0da71db836d73ad9954ff9880a593406e203b72d7`.
- Public handoff v2 tar SHA256
  `274620193c7d71cf391f9f064ad539ae3ec0ab8edef299ef7ccd07321e3f579c`;
  manifest SHA256
  `c63c895d968db91432199d12b2617bac5c36b97b08007a995cd9c1fb44db96d6`.

Both supervisors independently accepted these exact main bytes, 30 required
Docker cases and 32 canonical public handoff members for the same bounded
native-local Linux Docker profile. Qualified Podman remains unsupported
(11 ordinary passes / 19 blocked). This does not extend that accepted profile.
The copied historical Brine caller receipt is provenance only, **not** the
receiving host's daemon identity, authorization or application evidence.

The v1 script/test/pin and historical documentation from PR90/259 are preserved
byte-for-byte. V1 does not select v2 automatically; v2 never autodetects v1 or
accepts an arbitrary `--pin`. The provider protocols remain `dill.operation/1`,
`dill.preflight/1` and `dill.preflight-capabilities/1`; input/result and
qualification/handoff versions alone are explicitly v2.

## Offline validation

An authorized caller obtains the **exact official artifact** and authenticates
its API metadata separately. Local metadata JSON does not authenticate its own
origin. No latest/run resolution, network fetch, fallback/rebuild or expired-input
substitution occurs in this tool.

```sh
python3 scripts/prepare-alpha-pair-v2.py \
  --artifact-zip /absolute/private/accepted-main-docker.zip \
  --artifact-metadata /absolute/private/official-artifact-metadata.json \
  --validate-only
```

The API id/name/size/digest/workflow run/main/head SHA must match the source pin.
`expired` must be literal JSON false and `expires_at` must exactly match the
mandatory pin `expiresAt`. Current UTC must be **strictly before** that absolute
expiry, including a final recheck before output. Stale `expired:false` metadata
cannot extend acceptance after expiry; exact-boundary, null/coerced/missing and
unknown pin fields reject. Metadata is not a newly issued execution permit.

The full anchored ZIP hash precedes reading payloads. The outer namespace must
be exactly the frozen 55 safe regular unencrypted filenames. The inner namespace
is exactly 32 sorted regular canonical tar members: prior 15 payloads, 15 named
preflight JSONs, named-preflight documentation and `handoff.json`. Every manifest
file hash and binary/Pkl/schema source pin is checked. Manifest root/caller maps
are closed; fixture version and all 30 unique required case IDs must match.
Declaring those cases is supply consistency, not rerunning or passing them.

Canonical headers, modes, ownership, timestamps, bounded sizes and full regenerated
gzip byte equality reject links, extras, hidden/trailing data and alternative
containers. No archive path is used for unrestricted filesystem extraction.
The directory `test/qualification/v1` and `docs/named-preflight-v1.md` intentionally
retain their names inside the v2 supply; no relabeling of old evidence occurs.

## Output and future extraction boundary

Success returns a closed `dill.brine-alpha-input-result/2` JSON object with status
`verified-dill-input`, pinned identities/hashes/platform and always:

```json
{"jointApplicationAcceptance":"not-run","receivingTargetQualified":false,"payloadExtracted":false}
```

The last boolean is true only for explicit successful extraction. No daemon
identity, private path, source proof, registry authority or application-pass
field is invented. Failure exits 1 with only `DILL_PAIR_INPUT_REJECTED` on stderr
and no success JSON, including rejected CLI arguments.

`--output /absolute/private/new-directory` is mutually exclusive with
`--validate-only`. All checks finish before creating the destination. Its
existing real, symlink-free parent must be owned by the current user with no
group/other permissions. Existing destinations (including broken symlinks) are
never overwritten. All created directories are 0700, binaries 0500, other files
0400. An extraction I/O failure may leave a private incomplete directory; it
never reports success and will not overwrite it on retry. Do not treat partial
files as accepted. Same-UID hostile filesystem mutation is not an OS sandbox.

**Actual downloaded Linux payloads are local validate-only inputs.** Extraction
unit tests use synthetic nonexecutables only. Any future actual receiver needs
its separately accepted trusted provisioning before every candidate execution,
including version/discovery probes. This tool does not implement that receiver.

## Checks

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts \
  -p 'test_prepare_alpha_pair*.py' -v
```

The old eleven tests remain unchanged. V2 checks synthetic positive/extraction
and every created mode; expiry and final-clock boundary; closed typed pin/API/
manifest/caller maps; all hash anchors; 30-case consistency; cross-version
refusal; ZIP/tar paths, duplicates, headers, links, order and trailing data;
output-parent and no-overwrite rules; fixed CLI errors and no arbitrary pin.
These are not original source-boundary, native engine or actual application
proof. Driver-branch CI artifacts must never replace the accepted provider2d48
artifact referenced above. Release/publication and bundled v0.7 compatibility
remain separate.
