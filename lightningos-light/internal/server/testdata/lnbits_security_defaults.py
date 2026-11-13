"""Run inside the disposable LNbits container; credentials enter via stdin only."""
import base64
import hashlib
import hmac
import json
import sqlite3
import struct
import sys
import time

import httpx

login = json.load(sys.stdin)
client = httpx.Client(base_url="http://127.0.0.1:5000", timeout=20)

def call(method, path, payload=None, status=200):
    response = client.request(method, path, json=payload)
    assert response.status_code == status, (method, path, response.status_code)
    return response.json()

def sign_in():
    client.cookies.clear()
    return call("POST", "/api/v1/auth", login)

sign_in()
state = call("GET", "/api/v1/auth/2fa/status")
assert state["available"] and not state["mandatory"] and not state["enrolled"]
with sqlite3.connect("file:/app/data/database.sqlite3?mode=ro", uri=True) as db:
    assert db.execute("select count(*) from installed_extensions").fetchone()[0] == 0
    assert db.execute("select count(*) from extensions").fetchone()[0] == 0
settings = call("GET", "/admin/api/v1/settings")
assert settings["lnbits_user_default_extensions"] == []

setup = call("POST", "/api/v1/auth/2fa/setup")
key = base64.b32decode(setup["secret"])
digest = hmac.new(key, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
offset = digest[-1] & 15
code = str((struct.unpack(">I", digest[offset:offset+4])[0] & 0x7fffffff) % 1000000).zfill(6)
enrollment = call("POST", "/api/v1/auth/2fa/confirm", {"code": code})
codes = enrollment["recovery_codes"]
assert len(codes) == 10
call("POST", "/api/v1/auth/2fa/recovery/acknowledge")
assert sign_in()["two_factor_required"]
call("GET", "/api/v1/auth", status=401)
call("POST", "/api/v1/auth/2fa/verify", {"code": "badcode"}, status=401)
call("POST", "/api/v1/auth/2fa/verify", {"code": codes[0]})
call("GET", "/api/v1/auth")
assert call("GET", "/api/v1/auth/2fa/status")["recovery_remaining"] == 9
assert sign_in()["two_factor_required"]
call("POST", "/api/v1/auth/2fa/verify", {"code": codes[0]}, status=401)
call("POST", "/api/v1/auth/2fa/verify", {"code": codes[1]})
call("DELETE", "/api/v1/auth/2fa")
assert not call("GET", "/api/v1/auth/2fa/status")["enrolled"]
print("No installed/user extensions; optional TOTP enrollment, login challenge, invalid code rejection, recovery and replay rejection passed")
