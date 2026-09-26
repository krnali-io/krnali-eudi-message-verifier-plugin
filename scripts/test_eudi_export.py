"""Exercise the export CLI's input and file boundaries with untrusted fixtures."""
import base64
import json
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("unpack-eudi-export.py")


def b64(raw):
    return base64.urlsafe_b64encode(raw).decode().rstrip("=")


def fixture_jwt(**changes):
    # Syntactically shaped only: no genuine key, certificate or signature.
    header = {"typ": "rc-wrp+jwt", "alg": "ES256", "x5c": ["MAA="]}
    header.update(changes)
    return ".".join([b64(json.dumps(header).encode()), b64(b'{"sub":"untrusted-fixture"}'), b64(bytes(64))]).encode()


class ExportCLI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.source = self.root / "response.json"
        self.output = self.root / "result"

    def run_export(self, content, kind="registration", **extra):
        response = {"status": "success", "code": 200, "data": {"file_base64": base64.urlsafe_b64encode(content).decode()}}
        response.update(extra)
        self.source.write_text(json.dumps(response))
        return subprocess.run([sys.executable, str(SCRIPT), kind, str(self.source), "--out", str(self.output)], capture_output=True)

    def test_registry_jwt_is_unwrapped_without_printing_content(self):
        token = fixture_jwt()
        result = self.run_export(token)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.output.read_bytes(), token + b"\n")
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o600)
        self.assertNotIn(token, result.stdout + result.stderr)

    def test_p12_bytes_are_preserved(self):
        raw = b"\x30\x03\x02\x01\x03"  # DER-shaped fixture, not a usable P12.
        self.assertEqual(self.run_export(raw, "p12").returncode, 0)
        self.assertEqual(self.output.read_bytes(), raw)

    def test_existing_credentials_are_not_overwritten(self):
        self.output.write_bytes(b"existing material")
        self.assertNotEqual(self.run_export(fixture_jwt()).returncode, 0)
        self.assertEqual(self.output.read_bytes(), b"existing material")

    def test_symlink_target_is_not_overwritten(self):
        target = self.root / "target"
        target.write_bytes(b"existing material")
        self.output.symlink_to(target)
        self.assertNotEqual(self.run_export(fixture_jwt()).returncode, 0)
        self.assertEqual(target.read_bytes(), b"existing material")

    def test_wrong_profile_or_missing_chain_writes_nothing(self):
        for change in ({"typ": "JWT"}, {"alg": "none"}, {"x5c": []}):
            with self.subTest(change=change):
                self.assertNotEqual(self.run_export(fixture_jwt(**change)).returncode, 0)
                self.assertFalse(self.output.exists())

    def test_failed_registry_response_writes_nothing(self):
        self.assertNotEqual(self.run_export(fixture_jwt(), status="error", code=401).returncode, 0)
        self.assertFalse(self.output.exists())

    def test_oversized_export_writes_nothing(self):
        self.assertNotEqual(self.run_export(b"x" * (2 * 1024 * 1024)).returncode, 0)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
