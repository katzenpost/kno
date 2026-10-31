import os
from pathlib import Path
import subprocess
import sys
import time


def main():
    label, *command = sys.argv[1:]
    start = time.monotonic()
    result = subprocess.run(command)
    elapsed = time.monotonic() - start
    line = f"{label}: {elapsed:.1f}s (exit {result.returncode})"
    print(line, flush=True)
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as stream:
            stream.write(f"- {line}\n")
    return result.returncode if result.returncode >= 0 else 128 - result.returncode


if __name__ == "__main__":
    sys.exit(main())
