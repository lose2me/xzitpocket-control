#!/usr/bin/env python3
"""Deploy the acme.sh certificate for dl.xuda.live to Tencent Cloud CDN.

Invoked by acme.sh after issue/renew (--reloadcmd). Reads the API credentials
from the control .env and binds the PEM certificate to the CDN domain.
"""
import datetime
import hashlib
import hmac
import json
import os
import sys
import time
import urllib.request

ENV_PATH = "/opt/xzitpocket-control/data/.env"
CERT_DIR = "/opt/xzitpocket-control/certs"
DOMAIN = sys.argv[1] if len(sys.argv) > 1 else "dl.xuda.live"
CERT_PEM = os.path.join(CERT_DIR, DOMAIN + ".pem")
CERT_KEY = os.path.join(CERT_DIR, DOMAIN + ".key")


def read_env(path):
    values = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            values[key.strip()] = value.strip()
    return values


def tc3(service, host, action, version, payload, secret_id, secret_key):
    ts = int(time.time())
    date = datetime.datetime.utcfromtimestamp(ts).strftime("%Y-%m-%d")

    def digest(key, message):
        return hmac.new(key, message.encode(), hashlib.sha256).digest()

    content_type = "application/json; charset=utf-8"
    canonical_headers = (
        f"content-type:{content_type}\nhost:{host}\nx-tc-action:{action.lower()}\n"
    )
    signed_headers = "content-type;host;x-tc-action"
    canonical_request = (
        f"POST\n/\n\n{canonical_headers}\n{signed_headers}\n"
        f"{hashlib.sha256(payload.encode()).hexdigest()}"
    )
    string_to_sign = (
        f"TC3-HMAC-SHA256\n{ts}\n{date}/{service}/tc3_request\n"
        f"{hashlib.sha256(canonical_request.encode()).hexdigest()}"
    )
    secret_date = digest(("TC3" + secret_key).encode(), date)
    secret_service = digest(secret_date, service)
    secret_signing = digest(secret_service, "tc3_request")
    signature = hmac.new(secret_signing, string_to_sign.encode(), hashlib.sha256).hexdigest()

    headers = {
        "Authorization": (
            f"TC3-HMAC-SHA256 Credential={secret_id}/{date}/{service}/tc3_request, "
            f"SignedHeaders={signed_headers}, Signature={signature}"
        ),
        "Content-Type": content_type,
        "Host": host,
        "X-TC-Action": action,
        "X-TC-Timestamp": str(ts),
        "X-TC-Version": version,
    }
    request = urllib.request.Request(
        f"https://{host}/", data=payload.encode(), headers=headers, method="POST"
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.loads(response.read().decode())


def main():
    env = read_env(ENV_PATH)
    secret_id = env.get("CONTROL_COS_SECRET_ID", "")
    secret_key = env.get("CONTROL_COS_SECRET_KEY", "")
    if not secret_id or not secret_key:
        print("missing CONTROL_COS_SECRET_ID / CONTROL_COS_SECRET_KEY", file=sys.stderr)
        return 1

    with open(CERT_PEM, encoding="utf-8") as handle:
        certificate = handle.read()
    with open(CERT_KEY, encoding="utf-8") as handle:
        private_key = handle.read()

    uploaded = tc3(
        "ssl",
        "ssl.tencentcloudapi.com",
        "UploadCertificate",
        "2019-12-05",
        json.dumps({
            "CertificatePublicKey": certificate,
            "CertificatePrivateKey": private_key,
            "Alias": DOMAIN,
            "Repeatable": False,
        }),
        secret_id,
        secret_key,
    )
    if "Error" in uploaded.get("Response", {}):
        print("upload certificate failed: " + json.dumps(uploaded, ensure_ascii=False), file=sys.stderr)
        return 1
    cert_id = uploaded["Response"]["CertificateId"]

    bound = tc3(
        "cdn",
        "cdn.tencentcloudapi.com",
        "UpdateDomainConfig",
        "2018-06-06",
        json.dumps({
            "Domain": DOMAIN,
            "Https": {"Switch": "on", "CertInfo": {"CertId": cert_id}},
            "ForceRedirect": {"Switch": "on", "RedirectType": "https"},
        }),
        secret_id,
        secret_key,
    )
    if "Error" in bound.get("Response", {}):
        print("bind certificate failed: " + json.dumps(bound, ensure_ascii=False), file=sys.stderr)
        return 1
    print(f"cdn certificate bound: {cert_id} ({uploaded['Response'].get('ExpireTime', '')})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
