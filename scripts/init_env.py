#!/usr/bin/env python3
"""Create .env from .env.example with a freshly generated ENGINE_TOKEN (never overwrites)."""
import pathlib
import secrets
import sys

root = pathlib.Path(__file__).resolve().parent.parent
env, example = root / ".env", root / ".env.example"
if env.exists():
    print(".env already exists; leaving it unchanged")
    sys.exit(0)
text = example.read_text().replace("change-me-to-a-long-random-string", secrets.token_hex(32))
env.write_text(text)
env.chmod(0o600)
print("created .env with a random ENGINE_TOKEN")
