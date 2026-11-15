"""One-shot startup gate, not a runtime watchdog or a payment retry loop."""

import http.client
import json
import os
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never forward the macaroon to a redirect target, even on localhost.
        return None


def lnd_ready(endpoint, certificate, macaroon):
    url = urllib.parse.urlsplit(endpoint)
    if (url.scheme != "https" or not url.hostname or url.username is not None
            or url.password is not None or url.query or url.fragment
            or url.path not in ("", "/")):
        raise ValueError("invalid managed REST endpoint")
    with open(macaroon, "rb") as source:
        credential = source.read(65537)
    if not credential or len(credential) > 65536:
        raise ValueError("invalid managed credential")
    # Re-read TLS material on each attempt, so an atomic certificate refresh
    # by the broker during a long LND startup does not pin an obsolete copy.
    context = ssl.create_default_context(cafile=certificate)
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}), NoRedirect(),
        urllib.request.HTTPSHandler(context=context),
    )
    request = urllib.request.Request(
        endpoint.rstrip("/") + "/v1/balance/channels",
        headers={"Grpc-Metadata-macaroon": credential.hex()}, method="GET",
    )
    with opener.open(request, timeout=5) as response:
        if response.status != 200:
            return False
        raw = response.read(65537)
        if len(raw) > 65536:
            return False
        result = json.loads(raw)
    # This is the same funding status RPC used by LndRestWallet. A successful
    # authenticated response means the wallet is unlocked; zero is valid.
    # Graph synchronization and a positive balance are NOT startup requirements.
    if not isinstance(result, dict) or isinstance(result.get("balance"), bool):
        return False
    balance = result.get("balance")
    if not isinstance(balance, (str, int)):
        return False
    return int(balance) >= 0


def main():
    attempt = 0
    while True:
        try:
            ready = lnd_ready(
                os.environ["LND_REST_ENDPOINT"],
                "/etc/lnd/tls.cert", "/etc/lnd/lnbits.macaroon",
            )
        except (OSError, ValueError, urllib.error.URLError,
                http.client.HTTPException):
            ready = False
        if ready:
            print("LightningOS: LND funding RPC ready; starting LNbits.", flush=True)
            # Replace the gate, retaining the normal LNbits executable, args,
            # signal handling and all upstream runtime safety mechanisms.
            os.execv("/app/.venv/bin/lnbits", [
                "/app/.venv/bin/lnbits", "--port", "5000", "--host", "0.0.0.0",
                "--forwarded-allow-ips=*",
            ])
            return
        if attempt % 12 == 0:
            # Never log the endpoint, response, exception or credential.
            print("LightningOS: waiting for authenticated LND funding RPC; "
                  "LNbits has not started yet.", flush=True)
        attempt += 1
        time.sleep(5)


if __name__ == "__main__":
    main()
