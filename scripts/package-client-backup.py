#!/usr/bin/env python3
"""Build the installable node backup/restore plugin ZIP."""

from __future__ import annotations

import hashlib
import json
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PLUGIN_DIR = ROOT / "plugins" / "client-backup"
MARKET_DIR = ROOT / "plugin-market"
CATALOG_PATH = MARKET_DIR / "v1.json"
PACKAGE_FILES = (
    "komari-plugin.json",
    "script.js",
    "icon.svg",
    "README.md",
    "web/index.html",
    "web/app.js",
    "web/style.css",
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

    if not CATALOG_PATH.is_file():
        raise SystemExit("plugin market catalog is missing: " + str(CATALOG_PATH))

    MARKET_DIR.mkdir(parents=True, exist_ok=True)
    output_path = MARKET_DIR / f"client-backup-{version}.zip"
    with zipfile.ZipFile(output_path, "w", compression=zipfile.ZIP_DEFLATED) as package:
        for name in PACKAGE_FILES:
            package.write(PLUGIN_DIR / name, arcname=name)

    catalog = json.loads(CATALOG_PATH.read_text(encoding="utf-8"))
    if catalog.get("schema") != 1 or not isinstance(catalog.get("plugins"), list):
        raise SystemExit("plugin market catalog must contain schema=1 and a plugins array")

    digest = hashlib.sha256(output_path.read_bytes()).hexdigest()
    plugin_entry = {
        "name": manifest["name"],
        "short": manifest["short"],
        "description": manifest["description"],
        "version": version,
        "author": manifest["author"],
        "komari": manifest["komari"],
        "url": manifest["url"],
        "download": "https://raw.githubusercontent.com/llovely45/komari-next/main/plugin-market/" + output_path.name,
        "sha256": digest,
    }
    plugins = catalog["plugins"]
    for index, item in enumerate(plugins):
        if item.get("short") == manifest["short"]:
            plugins[index] = plugin_entry
            break
    else:
        plugins.append(plugin_entry)
    CATALOG_PATH.write_text(json.dumps(catalog, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    print(output_path)
    print("SHA-256: " + digest)


if __name__ == "__main__":
    main()
