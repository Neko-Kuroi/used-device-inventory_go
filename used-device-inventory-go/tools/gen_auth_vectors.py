#!/usr/bin/env python3
"""Generate password-hash test vectors from the Python side.

Run with the Python version's virtualenv interpreter:

    ../used-device-inventory/.venv/bin/python tools/gen_auth_vectors.py

Outputs:
  internal/xcrypto/testdata/pbkdf2_vectors.json
  internal/xcrypto/testdata/scrypt_vectors.json
  internal/auth/testdata/werkzeug_hashes.json
      hashes made by Werkzeug itself; the Go Check must accept the
      right password and reject a wrong one for every entry.
"""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

PASSWORDS = ["password", "admin-pass-1", "パスワード123", "a b c", "x" * 64]


def main():
    from werkzeug.security import generate_password_hash

    pbkdf2_vectors = []
    for digest in ("sha256", "sha512", "sha1"):
        for password in PASSWORDS[:3]:
            for iters in (1, 2, 1000):
                salt = ("salt-" + digest).encode()
                dk = hashlib.pbkdf2_hmac(
                    digest, password.encode(), salt, iters, dklen=32)
                pbkdf2_vectors.append({
                    "digest": digest, "password": password,
                    "salt": salt.decode(), "iterations": iters,
                    "keylen": 32, "hex": dk.hex()})
    out = ROOT / "internal" / "xcrypto" / "testdata"
    out.mkdir(parents=True, exist_ok=True)
    (out / "pbkdf2_vectors.json").write_text(
        json.dumps(pbkdf2_vectors, ensure_ascii=False, indent=1),
        encoding="utf-8")

    scrypt_vectors = []
    cases = [
        ("password", b"salt", 16, 1, 1, 64),
        ("password", b"NaCl", 1024, 8, 16, 64),
        ("パスワード", b"pepper", 64, 4, 2, 48),
        ("admin-pass-1", b"0123456789abcdef", 32768, 8, 1, 64),
    ]
    for password, salt, n, r, p, dklen in cases:
        dk = hashlib.scrypt(password.encode(), salt=salt, n=n, r=r, p=p,
                            dklen=dklen, maxmem=256 * 1024 * 1024)
        scrypt_vectors.append({
            "password": password, "salt": salt.decode(), "n": n, "r": r,
            "p": p, "keylen": dklen, "hex": dk.hex()})
    (out / "scrypt_vectors.json").write_text(
        json.dumps(scrypt_vectors, ensure_ascii=False, indent=1),
        encoding="utf-8")

    hashes = []
    for password in PASSWORDS:
        hashes.append({"password": password,
                       "hash": generate_password_hash(password)})
    for password in PASSWORDS[:2]:
        hashes.append({"password": password, "hash": generate_password_hash(
            password, method="pbkdf2:sha256:1000")})
    hashes.append({"password": "legacy-pass",
                   "hash": generate_password_hash(
                       "legacy-pass", method="pbkdf2:sha256:260000")})
    out = ROOT / "internal" / "auth" / "testdata"
    out.mkdir(parents=True, exist_ok=True)
    (out / "werkzeug_hashes.json").write_text(
        json.dumps(hashes, ensure_ascii=False, indent=1), encoding="utf-8")
    print("wrote auth vectors")


if __name__ == "__main__":
    main()
