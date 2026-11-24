"""Back up a matching factory USB-C hw4 H2, optionally install X H51."""
import argparse
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import time

from trial import Supervisor
from usb.core import USBError

HERE = Path(__file__).resolve().parent
LAYOUT = "b36b06348c9a2ac34109596f84a83f082e8e32309b5f579c0fa9c5a78b45f1e1"
INSTALLER = "17275cea3423b951888288e85e557cfc48c6f54745bd1c64e224c4298859c890"


def check(path, expected):
    if hashlib.sha256(path.read_bytes()).hexdigest() != expected:
        raise SystemExit(f"Checksum mismatch: {path}")


def run(tool, *args):
    subprocess.run([sys.executable, HERE / tool, *args], check=True)


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("port", help="Linux USB topology path, e.g. 3-4.3")
    cli.add_argument("backup", type=Path, help="New local directory for this player's original boot regions")
    cli.add_argument("--install", type=Path, metavar="H51_INSTALLER", help="Apply X after a successful live backup and preflight")
    args = cli.parse_args()
    if args.backup.exists() or not args.backup.parent.is_dir():
        cli.error("Choose a new backup directory under an existing directory")
    for line in (HERE / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split()
        check(HERE / name, digest)
    if args.install:
        check(args.install, INSTALLER)
    run("ram_boot.py", args.port)
    for _ in range(80):
        try:
            sup = Supervisor(args.port)
            if sup.version == 5 and sup.status()["ddr"] == 1:
                break
        except (USBError, ValueError, FileNotFoundError):
            pass
        time.sleep(.25)
    else:
        raise SystemExit("H05 recovery did not appear in RAM")
    run("read_nand.py", args.port, args.backup, "--count", "1536", "--image", HERE / "nand-reader.bin")
    pages = args.backup / "pages.bin"
    os.sync()
    check(pages, LAYOUT)
    print("Factory boot and slot regions backed up; layout matches the tested hw4.", flush=True)
    if args.install:
        run("install_x_recovery.py", args.port, HERE / "recovery-05.bin", "--backup", pages, "--apply")
        run("install_x_recovery.py", args.port, HERE / "recovery-15.bin", "--backup", pages, "--upgrade-to", "15", "--after", "15", "--apply")
        run("slot_update.py", args.port, "install", "A", args.install, "51")


if __name__ == "__main__":
    main()
