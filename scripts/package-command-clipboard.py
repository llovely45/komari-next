#!/usr/bin/env python3
"""Build an installable ZIP for the optional command clipboard plugin."""

from __future__ import annotations

import json
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PLUGIN_DIR = ROOT / "plugins" / "command-clipboard"
PACKAGE_FILES = (
    "komari-plugin.json",
    "script.js",
    "browser.js",
    "style.css",
)


def main() -> None:
    manifest_path = PLUGIN_DIR / "komari-plugin.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    version = str(manifest.get("version", "")).strip()
    if not version or any(char in version for char in "/\\"):
        raise SystemExit("plugin manifest needs a safe, non-empty version")

    missing = [name for name in PACKAGE_FILES if not (PLUGIN_DIR / name).is_file()]
    if missing:
        raise SystemExit("plugin package is missing: " + ", ".join(missing))

    output_dir = ROOT / "dist"
    output_dir.mkdir(parents=True, exist_ok=True)
    output_path = output_dir / f"command-clipboard-{version}.zip"
    with zipfile.ZipFile(output_path, "w", compression=zipfile.ZIP_DEFLATED) as package:
        for name in PACKAGE_FILES:
            package.write(PLUGIN_DIR / name, arcname=name)

    print(output_path)


if __name__ == "__main__":
    main()
