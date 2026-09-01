#!/usr/bin/env python3
import base64
import hashlib
import sys
from pathlib import Path

artifact, version, output = Path(sys.argv[1]), sys.argv[2], Path(sys.argv[3])
data = artifact.read_bytes()
digest = base64.b64encode(hashlib.sha512(data).digest()).decode()
name = sys.argv[4] if len(sys.argv) > 4 else artifact.name
output.write_text(f'version: {version}\nfiles:\n  - url: {name}\n    sha512: {digest}\n    size: {len(data)}\npath: {name}\nsha512: {digest}\n')
