#!/usr/bin/env python3
"""Build a database populated by the Python version itself and dump
the values the Go service must reproduce from it.

Run with the Python version's virtualenv interpreter:

    ../used-device-inventory/.venv/bin/python tools/gen_service_golden.py

Outputs (into internal/service/testdata/):
  python_populated.db  a database created entirely through the
                       Python version's own routes
  golden.json          per-device totals, profits, inspection state
                       and history (action + detail), computed with
                       the Python version's own functions
"""
import json
import shutil
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PY_ROOT = ROOT.parent / "used-device-inventory"
OUT = ROOT / "internal" / "service" / "testdata"

sys.path.insert(0, str(PY_ROOT))
sys.path.insert(0, str(PY_ROOT / "tests"))


def main():
    from conftest import DEVICE
    from device_app import (create_app, expense_total, gross_profit,
                            inspection_state, planned_profit)
    from device_app.db import get_db

    tmp = Path(tempfile.mkdtemp())
    db_path = tmp / "golden.db"
    app = create_app({"TESTING": True, "SECRET_KEY": "test",
                      "DATABASE": str(db_path)})
    client = app.test_client()
    client.post("/setup", data={"username": "admin",
                                "password": "admin-pass-1"})

    # Device 1: the conftest phone, inspected, sold, with expenses.
    client.post("/devices/new", data=DEVICE)
    client.post("/devices/1/inspection", data={
        f"result_{k}": "pass" for k in
        ["power", "screen_touch", "camera", "speaker_mic", "buttons",
         "charging", "wifi", "mobile", "biometrics", "battery"]})
    client.post("/devices/1/expenses", data={
        "category": "parts", "amount": "1500",
        "expense_date": "2026-11-03", "note": "バッテリー交換"})
    client.post("/devices/1/expenses", data={
        "category": "shipping", "amount": "800",
        "expense_date": "2026-11-10", "note": ""})
    for to in ("inspecting", "ready"):
        client.post("/devices/1/status", data={"to_status": to})
    client.post("/devices/1/edit", data=dict(
        DEVICE, sale_date="2026-11-12", sale_channel="店舗",
        sale_price="42000"))
    client.post("/devices/1/status", data={"to_status": "sold"})

    # Device 2: a PC, partially inspected (one fail), still stocked.
    pc = dict(DEVICE, kind="pc", maker="Lenovo", model="ThinkPad X1",
              serial_number="SN0002", imei="", network_restriction="",
              cpu="Core i7", ram="16GB", storage="512GB")
    client.post("/devices/new", data=pc)
    client.post("/devices/2/inspection", data={
        "result_power": "pass", "result_display": "fail",
        "result_keyboard": "na", "note_display": "横線あり"})

    # Device 3: a tablet moved to ready.
    tablet = dict(DEVICE, kind="tablet", maker="Apple", model="iPad",
                  serial_number="SN0003", imei="",
                  network_restriction="")
    client.post("/devices/new", data=tablet)
    client.post("/devices/3/inspection", data={
        f"result_{k}": "pass" for k in
        ["power", "screen_touch", "camera", "speaker_mic", "buttons",
         "charging", "wifi", "battery"]})
    client.post("/devices/3/status", data={"to_status": "inspecting"})
    client.post("/devices/3/status", data={"to_status": "ready"})

    with app.app_context():
        db = get_db()
        devices = db.execute("SELECT * FROM device ORDER BY id").fetchall()
        dump = []
        for d in devices:
            total = expense_total(db, d["id"])
            state = inspection_state(db, d)
            history = db.execute(
                "SELECT action, detail FROM history WHERE device_id = ?"
                " ORDER BY id", (d["id"],)).fetchall()
            dump.append({
                "id": d["id"], "kind": d["kind"], "status": d["status"],
                "imei": d["imei"],
                "network_restriction": d["network_restriction"],
                "inspection_done": d["inspection_done"],
                "data_erasure_by": d["data_erasure_by"],
                "expense_total": total,
                "gross_profit": gross_profit(d, total),
                "planned_profit": planned_profit(d, total),
                "inspection": {
                    "done": state["done"], "total": state["total"],
                    "complete": state["complete"], "fails": state["fails"]},
                "history": [{"action": h["action"], "detail": h["detail"]}
                            for h in history],
            })
    OUT.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(db_path, OUT / "python_populated.db")
    (OUT / "golden.json").write_text(
        json.dumps(dump, ensure_ascii=False, indent=1), encoding="utf-8")
    shutil.rmtree(tmp)
    print("wrote service golden")


if __name__ == "__main__":
    main()
