#!/usr/bin/env python3
"""Generate the db package's test fixtures from the Python version.

Run with the Python version's virtualenv interpreter, from anywhere:

    ../used-device-inventory/.venv/bin/python tools/gen_db_fixtures.py

Outputs (into internal/db/testdata/):
  python_v5.db       a database created by the Python app itself,
                     with one admin user (admin / admin-pass-1)
  v1.db              a database in the original v1 schema, built with
                     the same script as the Python migration test
  python_schema.txt  sqlite_master dump of python_v5.db plus its
                     user_version, to compare a Go-created schema with
"""
import sqlite3
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PY_ROOT = ROOT.parent / "used-device-inventory"
OUT = ROOT / "internal" / "db" / "testdata"

sys.path.insert(0, str(PY_ROOT))

V1_SCRIPT = """
CREATE TABLE user (id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')));
CREATE TABLE device (id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL, maker TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '', serial_number TEXT NOT NULL UNIQUE,
    imei TEXT, storage TEXT NOT NULL DEFAULT '',
    ram TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT '',
    battery_health INTEGER, sim_status TEXT NOT NULL DEFAULT '',
    activation_lock TEXT NOT NULL DEFAULT '',
    network_restriction TEXT NOT NULL DEFAULT '',
    cpu TEXT NOT NULL DEFAULT '', gpu TEXT NOT NULL DEFAULT '',
    os_name TEXT NOT NULL DEFAULT '', battery_cycles INTEGER,
    condition_rank TEXT NOT NULL DEFAULT 'B', status TEXT NOT NULL DEFAULT 'stocked',
    cosmetic_damage TEXT NOT NULL DEFAULT '', accessories TEXT NOT NULL DEFAULT '',
    purchase_date TEXT NOT NULL DEFAULT '', purchase_source TEXT NOT NULL DEFAULT '',
    purchase_price INTEGER, planned_price INTEGER,
    sale_date TEXT NOT NULL DEFAULT '', sale_channel TEXT NOT NULL DEFAULT '',
    sale_price INTEGER, warranty_days INTEGER, notes TEXT NOT NULL DEFAULT '',
    created_by INTEGER, created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')));
CREATE TABLE history (id INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id INTEGER NOT NULL, user_id INTEGER,
    action TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')));
PRAGMA user_version = 1;
"""


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    from werkzeug.security import generate_password_hash

    from device_app import create_app

    v5 = OUT / "python_v5.db"
    v5.unlink(missing_ok=True)
    app = create_app({"TESTING": True, "SECRET_KEY": "fixture",
                      "DATABASE": str(v5)})
    with app.app_context():
        from device_app.db import get_db
        db = get_db()
        db.execute(
            "INSERT INTO user (username, password_hash, is_admin)"
            " VALUES (?, ?, 1)",
            ("admin", generate_password_hash("admin-pass-1")))
        db.commit()

    conn = sqlite3.connect(v5)
    rows = conn.execute(
        "SELECT type, name, tbl_name, sql FROM sqlite_master"
        " WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name").fetchall()
    version = conn.execute("PRAGMA user_version").fetchone()[0]
    conn.close()
    with open(OUT / "python_schema.txt", "w", encoding="utf-8") as f:
        f.write(f"user_version={version}\n")
        for r in rows:
            f.write("|".join("" if c is None else c for c in r) + "\n")

    v1 = OUT / "v1.db"
    v1.unlink(missing_ok=True)
    conn = sqlite3.connect(v1)
    conn.executescript(V1_SCRIPT)
    conn.commit()
    conn.close()

    print(f"wrote fixtures to {OUT}")


if __name__ == "__main__":
    main()
