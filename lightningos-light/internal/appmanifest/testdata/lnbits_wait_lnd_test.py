"""Run with Python 3; no LND, Docker, secrets or network access needed."""
import contextlib
import io
import json
from pathlib import Path
import runpy
import ssl
import unittest
import urllib.error
import urllib.request
from unittest.mock import MagicMock, patch

gate = runpy.run_path(str(Path(__file__).resolve().parents[1] / "assets/lnbits_wait_lnd.py"))


class ReadinessTests(unittest.TestCase):
    def call(self, body=b'{"balance":"0"}', status=200):
        response = MagicMock()
        response.__enter__.return_value = response
        response.status = status
        response.read.return_value = body
        opener = MagicMock()
        opener.open.return_value = response
        with patch("builtins.open", unittest.mock.mock_open(read_data=b"dedicated-test")), \
                patch("ssl.create_default_context") as tls, \
                patch("urllib.request.build_opener", return_value=opener) as build:
            ready = gate["lnd_ready"]("https://127.0.0.1:18080/", "cert", "mac")
        tls.assert_called_once_with(cafile="cert")
        handlers = build.call_args.args
        self.assertEqual(handlers[0].proxies, {})
        self.assertIsInstance(handlers[1], gate["NoRedirect"])
        req = opener.open.call_args.args[0]
        self.assertEqual(req.full_url, "https://127.0.0.1:18080/v1/balance/channels")
        self.assertEqual(req.method, "GET")
        self.assertEqual(req.get_header("Grpc-metadata-macaroon"), b"dedicated-test".hex())
        self.assertEqual(opener.open.call_args.kwargs["timeout"], 5)
        if status == 200:
            response.read.assert_called_once_with(65537)
        else:
            response.read.assert_not_called()
        return ready

    def test_ready_zero_and_nonzero_balance(self):
        for value in ["0", 0, "12345"]:
            with self.subTest(value=value):
                self.assertTrue(self.call(json.dumps({"balance": value}).encode()))

    def test_invalid_balance_and_errors_never_release_gate(self):
        for value in [None, True, -1, "-1", 1.2, [], {}]:
            with self.subTest(value=value):
                self.assertFalse(self.call(json.dumps({"balance": value}).encode()))
        for body in [b"{}", b"[]", b"x" * 65537]:
            self.assertFalse(self.call(body))
        self.assertFalse(self.call(status=401))
        with self.assertRaises(ValueError):
            self.call(b"not-json")

    def test_redirects_cannot_forward_credential(self):
        handler = gate["NoRedirect"]()
        for code in (301, 302, 303, 307, 308):
            self.assertIsNone(handler.redirect_request(None, None, code, "", {}, "https://other/"))

    def test_invalid_endpoints_rejected_before_reading_credential(self):
        with patch("builtins.open") as read:
            for endpoint in ["http://127.0.0.1/", "https://user:pass@localhost/", "https:///",
                             "https://localhost/path", "https://localhost/?token=secret",
                             "https://localhost/#fragment"]:
                with self.assertRaises(ValueError):
                    gate["lnd_ready"](endpoint, "cert", "mac")
            read.assert_not_called()

    def test_bad_certificate_is_not_bypassed(self):
        with patch("builtins.open", unittest.mock.mock_open(read_data=b"test")), \
                patch("ssl.create_default_context", side_effect=ssl.SSLError("untrusted")):
            with self.assertRaises(ssl.SSLError):
                gate["lnd_ready"]("https://localhost/", "cert", "mac")

    def test_empty_or_oversized_credential_never_reaches_network(self):
        for raw in [b"", b"x" * 65537]:
            with patch("builtins.open", unittest.mock.mock_open(read_data=raw)), \
                    patch("urllib.request.build_opener") as build:
                with self.assertRaises(ValueError):
                    gate["lnd_ready"]("https://localhost/", "cert", "mac")
                build.assert_not_called()

    def test_waits_past_upstream_retry_budget_then_executes_once(self):
        main = gate["main"]
        states = [ConnectionRefusedError("secret-url"), ssl.SSLError("secret-cert"),
                  urllib.error.HTTPError("secret-url", 401, "secret", {}, None),
                  ValueError("secret-body")] + [False] * 24 + [True]
        with patch.dict(main.__globals__, lnd_ready=MagicMock(side_effect=states)), \
                patch.dict("os.environ", {"LND_REST_ENDPOINT": "https://127.0.0.1:18080/"}), \
                patch("os.execv") as execute, patch("time.sleep") as sleep:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                main()
            self.assertEqual(sleep.call_count, len(states) - 1)
            self.assertTrue(all(call.args == (5,) for call in sleep.call_args_list))
            execute.assert_called_once_with("/app/.venv/bin/lnbits", [
                "/app/.venv/bin/lnbits", "--port", "5000", "--host", "0.0.0.0",
                "--forwarded-allow-ips=*",
            ])
            self.assertEqual(output.getvalue().count("waiting"), 3)
            self.assertNotIn("secret", output.getvalue())
            self.assertNotIn("https://", output.getvalue())

    def test_interruption_does_not_launch_or_retry_app(self):
        main = gate["main"]
        with patch.dict(main.__globals__, lnd_ready=MagicMock(return_value=False)), \
                patch.dict("os.environ", {"LND_REST_ENDPOINT": "https://localhost/"}), \
                patch("time.sleep", side_effect=KeyboardInterrupt), patch("os.execv") as execute:
            with contextlib.redirect_stdout(io.StringIO()), self.assertRaises(KeyboardInterrupt):
                main()
            execute.assert_not_called()


if __name__ == "__main__":
    unittest.main()
