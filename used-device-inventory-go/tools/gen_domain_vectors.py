#!/usr/bin/env python3
"""Generate domain test vectors and the constants dump from the
Python version (the specification).

Run with the Python version's virtualenv interpreter:

    ../used-device-inventory/.venv/bin/python tools/gen_domain_vectors.py

Outputs (into internal/domain/testdata/):
  constants.json     KINDS/STATUSES/TRANSITIONS/checklists/etc.
  needs_reason.json  needs_reason for every status pair
  blockers.json      ready_blockers for synthetic devices
  csv_safe.json      csv_safe cases (strings and non-strings)
  to_jst.json        to_jst cases
  profits.json       gross_profit / planned_profit cases
"""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PY_ROOT = ROOT.parent / "used-device-inventory"
OUT = ROOT / "internal" / "domain" / "testdata"

sys.path.insert(0, str(PY_ROOT))


def main():
    import device_app as app_mod

    OUT.mkdir(parents=True, exist_ok=True)
    constants = {
        "kinds": app_mod.KINDS,
        "ranks": app_mod.RANKS,
        "statuses": app_mod.STATUSES,
        "network_restriction": app_mod.NETWORK_RESTRICTION,
        "flow_order": app_mod.FLOW_ORDER,
        "main_order": app_mod.MAIN_ORDER,
        "transitions": {k: sorted(v)
                        for k, v in app_mod.TRANSITIONS.items()},
        "reason_pairs": sorted(list(p) for p in app_mod.REASON_PAIRS),
        "device_fields": app_mod.DEVICE_FIELDS,
        "int_fields": sorted(app_mod.INT_FIELDS),
        "checklists": {k: [list(item) for item in v]
                       for k, v in app_mod.CHECKLISTS.items()},
        "results": app_mod.RESULTS,
        "expense_categories": app_mod.EXPENSE_CATEGORIES,
        "photo_kinds": app_mod.PHOTO_KINDS,
        "max_photos": app_mod.MAX_PHOTOS_PER_DEVICE,
        "max_photo_bytes": app_mod.MAX_PHOTO_BYTES,
    }
    (OUT / "constants.json").write_text(
        json.dumps(constants, ensure_ascii=False, indent=1), encoding="utf-8")

    statuses = list(app_mod.STATUSES)
    reason = [{"current": c, "target": t,
               "want": app_mod.needs_reason(c, t)}
              for c in statuses for t in statuses]
    (OUT / "needs_reason.json").write_text(
        json.dumps(reason, indent=1), encoding="utf-8")

    blockers = []
    for done in (0, 1):
        for erasure in ("", "2026-11-01"):
            for restriction in ("", "○", "△", "×", "確認できず"):
                device = {"inspection_done": done,
                          "data_erasure_at": erasure,
                          "network_restriction": restriction}
                blockers.append({
                    "inspection_done": done, "data_erasure_at": erasure,
                    "network_restriction": restriction,
                    "want": app_mod.ready_blockers(device)})
    (OUT / "blockers.json").write_text(
        json.dumps(blockers, ensure_ascii=False, indent=1), encoding="utf-8")

    csv_inputs = ["abc", "=SUM(A1:A2)", "+81-3-0000", "-5", "@cmd",
                  " =padded", "\t=x", "　=x", "\x1c=x", "", "=", "a=b",
                  0, 123, None, -5]
    csv_cases = [{"input": v, "want": app_mod.csv_safe(v)}
                 for v in csv_inputs]
    (OUT / "csv_safe.json").write_text(
        json.dumps(csv_cases, ensure_ascii=False, indent=1), encoding="utf-8")

    jst_inputs = [
        "2026-11-21 15:13:26", "2026-11-21 20:00:00",
        "2026-12-31 15:00:00", "2028-02-28 15:00:00",
        "2026-11-21 00:00:00", None, "", "not a date", "2026-11-21",
        "2026-11-21T15:13:26", "2026-13-40 00:00:00",
        # The strict-form rule (2026-11-24): only the exact
        # zero-padded 19-character form converts, in both versions.
        "2026-11-21 15:13:26.5", "2026-1-1 1:2:3", "2026-1-01 01:02:03",
        "2026-11-1 1:02:03", "2028-2-29 15:00:00",
        "0000-01-01 00:00:00", "0001-01-01 00:00:00",
        "2026-02-30 00:00:00", "2026-11-21 24:00:00",
        "2026-11-21 15:13:60", "2026-11-21 15:13:26 ",
        "999-01-01 01:02:03", "02026-11-21 15:13:26",
        "2026-00-01 00:00:00", "2026-01-00 00:00:00",
        "2026-1-32 00:00:00",
        # +9 hours would pass year 9999: unchanged (Python raised an
        # OverflowError here before the 2026-11-24 fix).
        "9999-12-31 14:59:59", "9999-12-31 15:00:00", "9999-12-31 23:59:59",
    ]
    jst = [{"input": v, "want": app_mod.to_jst(v)} for v in jst_inputs]
    (OUT / "to_jst.json").write_text(
        json.dumps(jst, ensure_ascii=False, indent=1), encoding="utf-8")

    profits = []
    for sale, planned, purchase in [
            (1000, 900, 500), (None, 900, 500), (1000, None, 500),
            (1000, 900, None), (None, None, None), (0, 0, 0)]:
        device = {"sale_price": sale, "planned_price": planned,
                  "purchase_price": purchase}
        profits.append({
            "sale_price": sale, "planned_price": planned,
            "purchase_price": purchase, "total": 120,
            "gross": app_mod.gross_profit(device, 120),
            "planned_profit": app_mod.planned_profit(device, 120)})
    (OUT / "profits.json").write_text(
        json.dumps(profits, indent=1), encoding="utf-8")
    print("wrote domain vectors")


if __name__ == "__main__":
    main()
