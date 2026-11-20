#!/usr/bin/env python3
"""Run with python3 -I. Offline synthetic evidence, not a Dill restore test.

The selected temporary filesystem must support hard links for the alias
control. Distinct files there do not prove isolation across mounts, devices,
hosts, or failure domains. Reopen checks do not prove crash durability.
"""

from contextlib import closing
import os
from pathlib import Path
import sqlite3
from tempfile import TemporaryDirectory


def require_distinct(*paths):
    for index, path in enumerate(paths):
        if any(path.samefile(other) for other in paths[:index]):
            raise ValueError("source, backup, and destination must be distinct")


def verified_rows(db):
    if db.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
        raise RuntimeError("SQLite integrity check failed")
    return db.execute("SELECT id, value FROM samples ORDER BY id").fetchall()


def require_rows(db, expected, label):
    if verified_rows(db) != expected:
        raise RuntimeError(f"unexpected rows in {label}")


def main():
    with TemporaryDirectory(prefix="dill-sqlite-fixture-") as root:
        directories = [Path(root) / name for name in ("source", "backup", "destination")]
        for path in directories:
            path.mkdir(mode=0o700)
        require_distinct(*directories)
        source, backup, destination = [path / "catalog.sqlite" for path in directories]
        for path in (source, backup, destination):
            path.touch(mode=0o600, exist_ok=False)
        require_distinct(source, backup, destination)

        alias = directories[2] / "alias.sqlite"
        os.link(source, alias)
        try:
            require_distinct(source, backup, alias)
        except ValueError:
            pass
        else:
            raise RuntimeError("hard-link alias accepted")
        finally:
            alias.unlink()

        captured_rows = [(1, "synthetic-catalog-v1"), (2, "expected-at-capture")]
        current_rows = [(1, "source-next-version"), captured_rows[1]]
        with (
            closing(sqlite3.connect(source)) as db,
            closing(sqlite3.connect(backup)) as captured,
            closing(sqlite3.connect(destination)) as restored,
        ):
            db.execute("CREATE TABLE samples (id INTEGER PRIMARY KEY, value TEXT NOT NULL)")
            db.executemany("INSERT INTO samples VALUES (?, ?)", captured_rows)
            db.commit()
            db.backup(captured)
            db.execute("UPDATE samples SET value = ? WHERE id = 1", (current_rows[0][1],))
            db.commit()

            captured.backup(restored)

        require_distinct(source, backup, destination)
        with (
            closing(sqlite3.connect(source)) as db,
            closing(sqlite3.connect(backup)) as captured,
            closing(sqlite3.connect(destination)) as restored,
        ):
            require_rows(restored, captured_rows, "reopened restored snapshot")
            restored.execute("DELETE FROM samples WHERE id = 2")
            restored.commit()
            require_rows(restored, captured_rows[:1], "updated destination")
            require_rows(db, current_rows, "source")
            require_rows(captured, captured_rows, "backup")
        require_distinct(source, backup, destination)
        print("PASS: SQLite snapshot restore, integrity, and file separation")


if __name__ == "__main__":
    main()
