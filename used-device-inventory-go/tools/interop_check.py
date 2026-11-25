#!/usr/bin/env python3
"""Cross-implementation check: the Python version and the Go
version take turns on ONE shared instance directory.

Phase A: the Python version writes (admin, a device, a photo, an
expense, a status change) and exports a CSV.
Phase B: the Go binary reads everything back (including a
byte-identical CSV for the same state), verifies the photo bytes,
then writes its own device, photo and expense.
Phase C: the Python version reads the Go version's writes back.

Run with the Python version's virtualenv interpreter:

    ../used-device-inventory/.venv/bin/python tools/interop_check.py

Exits non-zero if any check fails.
"""
import http.cookiejar
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PY_ROOT = ROOT.parent / "used-device-inventory"
GO_BIN = ROOT / "dist" / "inventory"
PORT = 18237
BASE = f"http://127.0.0.1:{PORT}"

sys.path.insert(0, str(PY_ROOT))

FAILURES = []


def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + (f" ({detail})" if detail and not ok else ""))
    if not ok:
        FAILURES.append(name)


DEVICE = {
    "kind": "smartphone", "maker": "Apple", "model": "iPhone 12",
    "serial_number": "SN0001", "imei": "123456789012345",
    "storage": "128GB", "ram": "4GB", "color": "黒",
    "battery_health": "89", "sim_status": "SIMフリー",
    "activation_lock": "解除済み", "network_restriction": "○",
    "cpu": "", "gpu": "", "os_name": "iOS 17", "battery_cycles": "",
    "condition_rank": "A", "status": "stocked",
    "cosmetic_damage": "角に小さな傷", "accessories": "箱、ケーブル",
    "data_erasure_at": "2026-11-02", "data_erasure_method": "本体の初期化",
    "purchase_date": "2026-11-01", "purchase_source": "店頭買取",
    "purchase_price": "30000", "planned_price": "45000",
    "sale_date": "", "sale_channel": "", "sale_price": "",
    "warranty_days": "90", "notes": "相互運用テスト",
}
JPEG_A = b"\xff\xd8\xff\xe0" + b"python-photo-bytes"
PNG_B = b"\x89PNG\r\n\x1a\n" + b"go-photo-bytes"


def python_client(db_path):
    from device_app import create_app
    app = create_app({"TESTING": True, "SECRET_KEY": "interop",
                      "DATABASE": str(db_path)})
    return app.test_client()


def phase_a(db_path):
    client = python_client(db_path)
    r = client.post("/setup", data={"username": "admin",
                                    "password": "admin-pass-1"})
    check("A: python setup", r.status_code == 302)
    r = client.post("/devices/new", data=DEVICE)
    check("A: python creates device", r.status_code == 302 and
          r.headers["Location"].endswith("/devices/1"))
    import io
    r = client.post("/devices/1/photos", data={
        "kind": "front",
        "photo": (io.BytesIO(JPEG_A), "a.jpg")},
        content_type="multipart/form-data")
    check("A: python uploads photo", r.status_code == 302)
    r = client.post("/devices/1/expenses", data={
        "expense_date": "2026-11-03", "category": "parts",
        "amount": "3000", "note": "バッテリー交換"})
    check("A: python adds expense", r.status_code == 302)
    r = client.post("/devices/1/status", data={"to_status": "inspecting",
                                               "reason": ""})
    check("A: python moves status", r.status_code == 302)
    csv_a = client.get("/export.csv").data
    check("A: python csv captured", len(csv_a) > 100)
    return csv_a


class GoClient:
    def __init__(self):
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(self.jar),
            _NoRedirect())

    def request(self, method, path, data=None, headers=None):
        req = urllib.request.Request(BASE + path, data=data,
                                     method=method, headers=headers or {})
        try:
            with self.opener.open(req, timeout=10) as resp:
                return resp.status, dict(resp.headers), resp.read()
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers), e.read()

    def get(self, path):
        return self.request("GET", path)

    def csrf(self, path):
        _, _, body = self.get(path)
        m = re.search(rb'name="csrf_token" value="([^"]+)"', body)
        assert m, f"no csrf token on {path}"
        return m.group(1).decode()

    def post_form(self, path, form_page, fields):
        import urllib.parse
        fields = dict(fields)
        fields["csrf_token"] = self.csrf(form_page)
        data = urllib.parse.urlencode(fields).encode()
        return self.request("POST", path, data=data, headers={
            "Content-Type": "application/x-www-form-urlencoded"})

    def post_photo(self, device_id, kind, filename, data):
        boundary = "----interop-boundary"
        parts = []
        for name, value in [("csrf_token", self.csrf(f"/devices/{device_id}")),
                            ("kind", kind)]:
            parts.append(f"--{boundary}\r\nContent-Disposition: form-data; "
                         f'name="{name}"\r\n\r\n{value}\r\n'.encode())
        parts.append(f"--{boundary}\r\nContent-Disposition: form-data; "
                     f'name="photo"; filename="{filename}"\r\n'
                     f"Content-Type: application/octet-stream\r\n\r\n".encode())
        parts.append(data)
        parts.append(f"\r\n--{boundary}--\r\n".encode())
        return self.request("POST", f"/devices/{device_id}/photos",
                            data=b"".join(parts), headers={
                                "Content-Type": f"multipart/form-data; boundary={boundary}"})


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def phase_b(db_path, csv_a):
    env = dict(os.environ, DEVICE_DB=str(db_path))
    proc = subprocess.Popen(
        [str(GO_BIN), "serve", "--addr", f"127.0.0.1:{PORT}"],
        env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    try:
        client = GoClient()
        for _ in range(100):
            try:
                status, _, _ = client.get("/login")
                if status == 200:
                    break
            except Exception:
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("go server did not come up")
        status, headers, _ = client.post_form("/login", "/login", {
            "username": "admin", "password": "admin-pass-1"})
        check("B: go logs in with the python-created admin", status == 302)
        status, _, body = client.get("/")
        check("B: go list shows the python device",
              status == 200 and "iPhone 12".encode() in body and
              "写真 1枚".encode() in body)
        status, _, body = client.get("/devices/1")
        text = body.decode()
        check("B: go detail shows python's expense and history",
              "3,000 円" in text and "写真追加: 正面" in text and
              "検査中" in text)
        status, _, body = client.get("/export.csv")
        check("B: go csv is byte-identical to python's", body == csv_a)
        status, headers, body = client.get("/devices/1/photos/1")
        check("B: go serves python's photo bytes",
              status == 200 and body == JPEG_A)
        device2 = dict(DEVICE, serial_number="SN0002", maker="Sony",
                       model="Xperia", kind="pc", imei="",
                       network_restriction="")
        status, headers, _ = client.post_form("/devices/new", "/devices/new",
                                              device2)
        check("B: go creates device 2",
              status == 302 and headers.get("Location", "").endswith("/devices/2"))
        status, _, _ = client.post_form("/devices/1/expenses", "/devices/1", {
            "expense_date": "2026-11-04", "category": "shipping",
            "amount": "800", "note": ""})
        check("B: go adds an expense to device 1", status == 302)
        status, _, _ = client.post_photo(2, "back", "b.png", PNG_B)
        check("B: go uploads a photo to device 2", status == 302)
    finally:
        proc.terminate()
        proc.wait(timeout=10)


def phase_c(db_path):
    client = python_client(db_path)
    r = client.post("/login", data={"username": "admin",
                                    "password": "admin-pass-1"})
    check("C: python logs in after go's writes", r.status_code == 302)
    body = client.get("/devices/2").get_data(as_text=True)
    check("C: python shows go's device",
          "Xperia" in body and "Sony" in body and "仕入済み" in body)
    m = re.search(r"/devices/2/photos/(\d+)", body)
    r = client.get(f"/devices/2/photos/{m.group(1)}") if m else None
    check("C: python serves go's photo bytes",
          r is not None and r.status_code == 200 and r.data == PNG_B)
    body = client.get("/devices/1").get_data(as_text=True)
    check("C: python sees go's expense in the total",
          "3,800 円" in body and "経費追加: 送料 800円" in body)
    body = client.get("/").get_data(as_text=True)
    check("C: python list shows both devices",
          "iPhone 12" in body and "Xperia" in body)


def main():
    if not GO_BIN.exists():
        print(f"go binary not built: {GO_BIN}")
        return 1
    tmp = Path(tempfile.mkdtemp())
    try:
        db_path = tmp / "instance" / "devices.db"
        db_path.parent.mkdir(parents=True)
        csv_a = phase_a(db_path)
        phase_b(db_path, csv_a)
        phase_c(db_path)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    if FAILURES:
        print(f"\n{len(FAILURES)} check(s) failed")
        return 1
    print("\nall interop checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
