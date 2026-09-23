#!/usr/bin/env python3
"""Build the installable Go/WASI DDNS plugin ZIP."""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PLUGIN_DIR = ROOT / "plugins" / "ddns"
MANIFEST_PATH = PLUGIN_DIR / "komari-plugin.json"


def main() -> None:
    go = shutil.which("go")
    if not go:
        raise SystemExit("Go 1.25 or later is required to build the plugin")

    manifest = json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))
    short = str(manifest.get("short", "")).strip()
    version = str(manifest.get("version", "")).strip()
    if manifest.get("runtime") != "go-wasi" or manifest.get("entry") != "plugin.wasm":
        raise SystemExit("DDNS manifest must declare runtime go-wasi and entry plugin.wasm")
    if not short or not version or any(char in short + version for char in "/\\"):
        raise SystemExit("plugin manifest needs safe short and version values")

    with tempfile.TemporaryDirectory(prefix="komari-ddns-") as temp:
        wasm = Path(temp) / "plugin.wasm"
        environment = os.environ.copy()
        environment.update({"GOOS": "wasip1", "GOARCH": "wasm", "CGO_ENABLED": "0"})
        subprocess.run(
            [go, "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", str(wasm), "./plugins/ddns"],
            cwd=ROOT,
            env=environment,
            check=True,
        )
        manifest["entrySha256"] = hashlib.sha256(wasm.read_bytes()).hexdigest()
        MANIFEST_PATH.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

        files = [MANIFEST_PATH, PLUGIN_DIR / "icon.svg", PLUGIN_DIR / "README.md"]
        files.extend(sorted(path for path in (PLUGIN_DIR / "web").rglob("*") if path.is_file()))
        if any(not path.is_file() for path in files):
            raise SystemExit("plugin package is missing a required source file")

        output_dir = ROOT / "dist"
        output_dir.mkdir(parents=True, exist_ok=True)
        output = output_dir / f"{short}-{version}.zip"
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            entries = [(wasm, "plugin.wasm")]
            entries.extend((path, path.relative_to(PLUGIN_DIR).as_posix()) for path in files)
            for source, name in entries:
                entry = zipfile.ZipInfo(name, (2026, 1, 1, 0, 0, 0))
                entry.compress_type = zipfile.ZIP_DEFLATED
                entry.external_attr = 0o100644 << 16
                archive.writestr(entry, source.read_bytes())

    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    output.with_suffix(".zip.sha256").write_text(digest + "  " + output.name + "\n", encoding="utf-8")
    print(output)
    print("SHA256: " + digest)


if __name__ == "__main__":
    main()
