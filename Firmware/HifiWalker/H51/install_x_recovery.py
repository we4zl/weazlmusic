"""Install a tested X recovery stage into the two audited H2 ROM blocks."""
import argparse
from hashlib import sha256
import json
from pathlib import Path
import struct
import time
import zlib

import usb.core

from trial import Supervisor
from slot_update import player_status

TOKEN = 0x5732
BLOCK_BYTES = 64 * 2112
BACKUP_HASH = "4d95a30f60e243e40741f263f1e2ea06961ffd7854b99abad8ecefd4fc4a6d06"
FIELDS = ("build", "primary_crc", "copy_crc", "primary_matches", "copy_matches",
          "copy_empty", "last_page", "write_result", "inspect_result")


def report(dev, build):
    data = bytes(dev.ctrl_transfer(0xc0, 0x90, 0, 0, 36, timeout=6000))
    result = dict(zip(FIELDS, struct.unpack("<9I", data)))
    if result["build"] != build or result["inspect_result"]:
        raise RuntimeError(f"Installer could not identify and read the NAND: {result}")
    return result


def command(dev, request, target=0):
    dev.ctrl_transfer(0x40, request, target, TOKEN, timeout=6000)


def verify_after_write(dev, build):
    for _ in range(3):
        time.sleep(1)
        try:
            return report(dev, build)
        except usb.core.USBTimeoutError as error:
            timed_out = error
    raise timed_out


def log(event, result):
    print(json.dumps({"event": event, **result}), flush=True)


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("port")
    cli.add_argument("payload", type=Path)
    cli.add_argument("--backup", type=Path, required=True)
    cli.add_argument("--apply", action="store_true", help="Write the audited ROM blocks and restart")
    cli.add_argument("--upgrade-to", type=int, choices=(15,), help="Upgrade the two verified H05 copies")
    cli.add_argument("--after", type=int, default=5, help="Expected firmware version after restart")
    args = cli.parse_args()
    backup = args.backup.read_bytes()
    if len(backup) != 1536 * 2112 or sha256(backup).hexdigest() != "b36b06348c9a2ac34109596f84a83f082e8e32309b5f579c0fa9c5a78b45f1e1":
        cli.error("Factory NAND does not match the qualified hw4 layout")
    previous_crc = zlib.crc32(backup[:BLOCK_BYTES])
    version = args.upgrade_to or 5
    build = int.from_bytes(f"H{version:02}F".encode(), "big")
    if args.upgrade_to:
        previous_crc = 4156524267  # Read back after installing both H05 copies.
    sup = Supervisor(args.port)
    allowed = (5,)
    state = sup.status()
    if sup.version not in allowed or state["ddr"] != 1 or state["active"] == 0:
        cli.error(f"Expected a compatible H{version:02} installer host with free slot 0")
    if sup.version == 9:
        player = player_status(sup.usb)
        if player["playing"] or player["volume"]:
            cli.error("Pause and mute the player before using the recovery installer")
    payload = args.payload.read_bytes()
    if sup.stage(0, build, payload)["state"] != 2:
        raise RuntimeError("X rejected the installer")
    log("staged", {"sha256": sha256(payload).hexdigest()})
    sup.cmd(0x47)
    rebooting = False
    try:
        initial = report(sup.usb, build)
        log("preflight", initial)
        already_installed = (initial["primary_matches"] and initial["copy_matches"]
                             and initial["primary_crc"] == initial["copy_crc"])
        if not already_installed and initial["primary_crc"] != previous_crc:
            raise RuntimeError("The original primary block differs from the archived backup")
        known_copy = args.upgrade_to and initial["copy_crc"] == previous_crc
        if not (initial["copy_empty"] or initial["copy_matches"] or known_copy):
            raise RuntimeError("Secondary ROM block contents differ from the audited layout")
        if not args.apply:
            return
        if not already_installed:
            if not initial["copy_matches"]:
                command(sup.usb, 0x92, 448)
                copy = verify_after_write(sup.usb, build)
                log("secondary", copy)
                if copy["write_result"] or not copy["copy_matches"] or copy["primary_crc"] != previous_crc:
                    raise RuntimeError("The X secondary copy could not be verified")
            command(sup.usb, 0x92)
            primary = verify_after_write(sup.usb, build)
            log("primary", primary)
            if (primary["write_result"] or not primary["primary_matches"] or not primary["copy_matches"]
                    or primary["primary_crc"] != primary["copy_crc"]):
                raise RuntimeError("The two physical X recovery copies did not verify")
        rebooting = True
        try:
            command(sup.usb, 0x93)
        except usb.core.USBError as error:
            if error.errno not in (5, 19):
                raise
    finally:
        if not rebooting:
            sup.cmd(0x74)
    old_address = sup.usb.address
    cutoff = time.monotonic() + 20
    while time.monotonic() < cutoff:
        time.sleep(.2)
        try:
            current = Supervisor(args.port)
            if (current.usb.address != old_address and current.version == args.after
                    and current.status()["ddr"] == 1):
                player = {} if current.version in (5, 11, 13, 15) else player_status(current.usb)
                log("restarted", {"version": current.version, "address": current.usb.address,
                                  **current.status(), **player})
                if player.get("volume") or player.get("playing"):
                    raise RuntimeError("The restarted player did not boot paused and muted")
                return
        except (ValueError, usb.core.USBError):
            continue
    raise TimeoutError(f"A fresh H{args.after:02} attachment did not return after restarting from NAND")


if __name__ == "__main__":
    main()
