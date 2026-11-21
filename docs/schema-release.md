# Schema packaging and releases

`internal/loader/pkl-toolchain.json` is the single Pkl version and asset digest
source. Runtime embeds it; `scripts/install-pkl.py` consumes the same file for
CI, release and local installations. Digests for 0.31.1 were taken from the
`digest` fields of the official [Apple Pkl GitHub release API](https://api.github.com/repos/apple/pkl/releases/tags/0.31.1),
not calculated from the binary being installed. To upgrade, independently review
the upstream release and update the version and **all** supported asset digests
together, then run the package, example and design checks with the new binary.
The digests authenticate bytes relative to this checked-in trust decision; they
are not a separate signature or protection against a compromised upstream account.
Only listed platforms can use the managed downloader. Explicit `DILL_PKL_PATH`
and PATH installations remain operator-trusted. Cached managed binaries are
versioned and checked before use; legacy cache files are left untouched.
Legacy checksum and unverified-download environment variables now fail explicitly.

## Published URLs and migration

The README and `examples/remote/compose.pkl` reference
`package://pkg.ushd.ca/dill@0.5.0#/dill.pkl`. The public endpoint initially
returned HTTP 404 for metadata and its checksum, even though the registry
source metadata and GitHub archive matched their published checksums. On
2026-11-19 at 02:46 UTC, a recheck with Pkl 0.31.1 verified HTTPS metadata,
archive, checksums, and clean-cache evaluation. The remote example also
passed with a separate fresh cache. No registry or release changes were made
as part of this verification.

If discovery fails, use the checked-in schema examples and rerun
`PKL=/path/to/pinned/pkl python3 scripts/smoke-schema.py 0.5.0` before relying on
the package URI again. An available GitHub archive or a passing cached
example does not establish public package discovery. Do not republish an
existing version to fix an availability problem. These schema releases are
not the experimental roadmap model; no `@NEXT` package is promised.
Existing historical archives included the source tree; this change does not
alter or republish them. Users relying on non-schema files inside an old archive
must instead obtain those files from source; future archives contain only
`dill.pkl`. First-use Pkl package checksums do not authenticate the publisher.

## Release procedure and recovery

The tag-triggered workflow stages only `PklProject` and `dill.pkl` in a temporary
directory, checks the resulting zip's exact allowlist, and publishes release
assets without replacement. Before uploading assets or publishing discovery, it
verifies the local archive allowlist, both checksums, and the metadata's version,
URI, archive URL and archive digest. It then compares every existing asset byte-for-byte, uploads missing
assets, verifies them, and only then commits both discovery
metadata files to `umayr/pkl-registry` in one normal Git push. An existing
registry file with different bytes, pre-existing staged registry changes, a
changed release asset, a race, permission failure, or non-fast-forward push
fails closed. Publication is serialized within this repository; external writers
and manual edits are not made impossible by
GitHub or Git. Protect release assets, registry branch, and the registry token.

For CLI builds, the publisher generates `SHA256SUMS` from the exact `--binary`
inputs. It lists lowercase SHA-256 digests and asset basenames sorted by name,
and is checked and uploaded like any other immutable release asset. It is not
part of the Pkl package or registry. See the [README](../README.md#setup) for
user verification. Run the offline manifest regression tests with:

```sh
python3 -m unittest discover -s scripts -p 'test_release_binary_manifest.py'
```

Do not delete or replace existing assets to make a retry pass. Re-run the same
tag workflow with identical source/toolchain after a partial failure; it verifies
existing objects and fills missing ones. A mismatched version requires a new
version or operator investigation, never an overwrite. If a rebuild is not
byte-identical (including executable binaries), retry deliberately fails.

After discovery publication the workflow checks actual HTTPS metadata and its
checksum, fetches and checks the archive checksum, then evaluates a synthetic
module with an empty Pkl cache. CDN propagation is retried. A failing smoke
leaves published objects in place for investigation and safe retry, rather than
silently altering them. `PKL=/path/to/pkl python3 scripts/smoke-schema.py VERSION`
can perform the same read-only check for an already published release. No new
release has been published or smoke-verified as part of this change. Manual
catalog publication, mirrors, publisher authentication, and cross-repository
atomicity are not provided.

Run `make validate PKL=/path/to/pinned/pkl` for packaging, allowlist and
reproducibility checks, mocked immutable publication/retry tests, schema/example
evaluation and design fixtures. For #17, local packaging and simulated
immutable publication/retry checks passed with Pkl 0.31.1, as did the separate
clean-cache public smoke on recheck. No live publication or retry was attempted.
These checks do not prove engine runtime semantics.
