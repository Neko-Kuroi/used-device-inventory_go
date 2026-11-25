#!/usr/bin/env python3
"""Capture the Python version's real CSV output for the populated
golden database, to compare the Go export byte for byte.

Run with the Python version's virtualenv interpreter:

    ../used-device-inventory/.venv/bin/python tools/gen_csv_golden.py

Reads internal/service/testdata/python_populated.db (built by
gen_service_golden.py) and writes internal/web/testdata/csv_*.csv.
"""
import shutil
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PY_ROOT = ROOT.parent / "used-device-inventory"
SRC = ROOT / "internal" / "service" / "testdata" / "python_populated.db"
OUT = ROOT / "internal" / "web" / "testdata"

sys.path.insert(0, str(PY_ROOT))


def main():
    from device_app import create_app

    tmp = Path(tempfile.mkdtemp())
    db_path = tmp / "golden.db"
    shutil.copyfile(SRC, db_path)
    app = create_app({"TESTING": True, "SECRET_KEY": "test",
                      "DATABASE": str(db_path)})
    client = app.test_client()
    resp = client.post("/login", data={"username": "admin",
                                       "password": "admin-pass-1"})
    assert resp.status_code == 302, resp.status_code

    OUT.mkdir(parents=True, exist_ok=True)
    cases = {
        "csv_all.csv": "/export.csv",
        "csv_kind_pc.csv": "/export.csv?kind=pc",
        "csv_q_iphone.csv": "/export.csv?q=iPhone",
    }
    for name, url in cases.items():
        resp = client.get(url)
        assert resp.status_code == 200, (url, resp.status_code)
        (OUT / name).write_bytes(resp.data)
    shutil.rmtree(tmp)
    print("wrote csv golden")


if __name__ == "__main__":
    main()
