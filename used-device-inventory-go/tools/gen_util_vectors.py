#!/usr/bin/env python3
"""Generate util test vectors from CPython's own behaviour.

    python3 tools/gen_util_vectors.py        (any Python 3)

ToInt and PyStrip are defined by Python's int() and str.strip(), so
their vectors come from running those functions here. SafeNext is a
rule from the design (an internal path only), so its vectors are
computed from the rule as written there.
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "internal" / "util" / "testdata"

TOINT_INPUTS = [
    "0", "42", " 42 ", "+7", "-13", "007", "1_000", "1_0_0", "1__0",
    "_1", "1_", "", " ", "+", "-", "3.5", "1e3", "0x10", "12a",
    "１２３", "１_０００", "١٢٣", "१२३", "1２3", "＋１２", "−12",
    "　42　", "\x1c42\x1f", "\t-9\n", "9" * 25, "9223372036854775807",
    "9223372036854775808",
]

STRIP_INPUTS = [
    "abc", " abc ", "　abc　", "\x1cabc\x1f", " abc ",
    "​abc​", "　 abc \t", "a b", "", "　", " abc ",
]


def safe_next(nxt: str) -> str:
    if nxt.startswith("/") and not nxt.startswith("//") \
            and not nxt.startswith("/\\"):
        return nxt
    return "/"


SAFENEXT_INPUTS = [
    "/", "/devices", "/devices/3?x=1", "//evil.example", "/\\evil.example",
    "", "devices", "https://example.com/", "/a", "\\evil.example",
]


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    toint = []
    for s in TOINT_INPUTS:
        try:
            toint.append({"input": s, "ok": True, "value": str(int(s))})
        except ValueError:
            toint.append({"input": s, "ok": False, "value": ""})
    (OUT / "toint.json").write_text(
        json.dumps(toint, ensure_ascii=False, indent=1), encoding="utf-8")

    strip = [{"input": s, "want": s.strip()} for s in STRIP_INPUTS]
    (OUT / "strip.json").write_text(
        json.dumps(strip, ensure_ascii=False, indent=1), encoding="utf-8")

    nxt = [{"input": s, "want": safe_next(s)} for s in SAFENEXT_INPUTS]
    (OUT / "safe_next.json").write_text(
        json.dumps(nxt, ensure_ascii=False, indent=1), encoding="utf-8")
    print("wrote util vectors")


if __name__ == "__main__":
    main()
