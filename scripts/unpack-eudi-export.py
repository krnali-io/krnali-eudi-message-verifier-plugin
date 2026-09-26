#!/usr/bin/env python3
"""Unpack an EUDI registry API export without printing its contents.

This checks the export's shape, not its signature, trust chain or permissions.
It never contacts a service, installs Kubernetes secrets, or changes deployment.
"""
import argparse
import base64
import binascii
import json
import os
from pathlib import Path
import re

MAX_EXPORT = 2 * 1024 * 1024


def decode(value, *, url_only=False):
    alphabet = r"[A-Za-z0-9_-]+" if url_only else r"[A-Za-z0-9+/_-]+={0,2}"
    if not isinstance(value, str) or not re.fullmatch(alphabet, value):
        raise ValueError("Invalid base64 in export")
    try:
        return base64.b64decode(value + "=" * (-len(value) % 4), altchars=b"-_", validate=True)
    except (ValueError, binascii.Error) as exc:
        raise ValueError("Invalid base64 in export") from exc


def unpack(source, kind):
    with source.open("rb") as stream:
        raw = stream.read(MAX_EXPORT + 1)
    if len(raw) > MAX_EXPORT:
        raise ValueError("Export exceeds 2 MiB")
    try:
        envelope = json.loads(raw)
        if envelope["status"] != "success" or envelope["code"] != 200:
            raise ValueError("Registry response is not successful")
        content = decode(envelope["data"]["file_base64"])
    except (KeyError, TypeError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError("Expected the registry's JSON certificate response") from exc
    if kind == "p12":
        # Only a coarse DER envelope check; keytool/OpenSSL must inspect the P12.
        if not content.startswith(b"\x30"):
            raise ValueError("Export is not a DER PKCS#12 container")
        return content
    try:
        parts = content.decode("ascii").strip().split(".")
        if len(parts) != 3:
            raise ValueError("Registration export is not a compact JWT")
        header, claims = [json.loads(decode(p, url_only=True)) for p in parts[:2]]
        signature = decode(parts[2], url_only=True)
        sizes = {"ES256": 64, "ES384": 96, "ES512": 132}
        if not isinstance(header, dict) or not isinstance(claims, dict):
            raise ValueError("Registration JWT must contain JSON objects")
        if header.get("typ") != "rc-wrp+jwt" or header.get("alg") not in sizes:
            raise ValueError("Registration JWT type or algorithm does not match the pinned verifier")
        if len(signature) != sizes[header["alg"]]:
            raise ValueError("Registration JWT signature has the wrong size")
        chain = header.get("x5c")
        if not isinstance(chain, list) or not chain:
            raise ValueError("Registration JWT is missing its certificate chain")
        for cert in chain:
            if not decode(cert).startswith(b"\x30"):
                raise ValueError("Registration JWT has a malformed certificate chain")
    except (UnicodeError, TypeError, json.JSONDecodeError) as exc:
        raise ValueError("Malformed registration JWT") from exc
    return (".".join(parts) + "\n").encode("ascii")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("p12", "registration"))
    parser.add_argument("source", type=Path, help="JSON response saved from the registry API")
    parser.add_argument("--out", type=Path, required=True, help="New output file; never overwritten")
    args = parser.parse_args()
    try:
        content = unpack(args.source, args.kind)
        # The caller chooses a protected directory. O_EXCL also rejects symlinks.
        fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(content)
                stream.flush()
                os.fsync(stream.fileno())
        except BaseException:
            args.out.unlink(missing_ok=True)
            raise
    except FileExistsError:
        parser.exit(1, "Refusing to overwrite the output file\n")
    except (OSError, ValueError):
        parser.exit(1, "Export could not be unpacked; check its format, destination and access permissions\n")
    print(f"Wrote {args.out} with mode 0600. Certificate signature, trust and intended use still require verification.")


if __name__ == "__main__":
    main()
