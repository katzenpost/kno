import json
import os
from pathlib import Path
import subprocess
import sys
import time


def images():
    rows = json.loads(subprocess.check_output(["podman", "images", "--format", "json"]))
    return sorted({name for row in rows for name in (row.get("Names") or row.get("names") or [])
                   if name.split("/")[-1].startswith(("kno-build-", "kno-katzenpost-deb-", "kno-freebsd-"))})


def main():
    root = Path.home() / ".cache/katzenpost/ci-images"
    root.mkdir(parents=True, exist_ok=True)
    archive = root / "builders.tar"
    start = time.monotonic()
    if sys.argv[1] == "restore":
        if archive.exists():
            subprocess.run(["podman", "load", "--input", str(archive)], check=True)
            archive.unlink()
    else:
        names = images()
        if names:
            temporary = archive.with_suffix(".tmp")
            subprocess.run(["podman", "save", "--multi-image-archive", "--format", "docker-archive",
                            "--output", str(temporary), *names], check=True)
            os.replace(temporary, archive)
    print(f"Builder image cache {sys.argv[1]}: {time.monotonic() - start:.1f}s")


if __name__ == "__main__":
    main()
