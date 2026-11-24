"""Apply an X application package over normal USB and confirm the rebooted build."""
import argparse
from hashlib import sha256
import json
from pathlib import Path
import struct
import time

import usb.core

from trial import Supervisor

BUILD = 0x4832534c
TARGETS = {"A": 512, "B": 768}
LABELS = ("screen", "cursor", "tracks", "mount_error", "volume", "playing", "position", "bluetooth")


def player_status(dev):
    report = bytes(dev.ctrl_transfer(0xc0, 0x51, 0, 0, 64, timeout=2500))
    *fields, label = struct.unpack("<8I32s", report)
    return {**dict(zip(LABELS, fields)),
            "label": label.split(b"\0", 1)[0].decode(errors="replace")}


def emit(name, data):
    print(json.dumps({"event": name, **data}), flush=True)


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("port")
    cli.add_argument("operation", choices=("install", "rollback"))
    cli.add_argument("target", choices=TARGETS)
    cli.add_argument("installer", type=Path)
    cli.add_argument("expect", type=int, choices=(6, 7, 8, 9, 10, 12, 14, 16, 18, 19, 20, 21,
                                                 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
                                                 33, 34, 35, 36, 38, 39, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51))
    args = cli.parse_args()
    sup = Supervisor(args.port)
    state = sup.status()
    if sup.version < 5 or state["ddr"] != 1 or state["active"] == 0:
        cli.error("Expected X recovery or player with free trial region 0")
    if sup.version not in (5, 11, 13, 15):
        before = player_status(sup.usb)
        emit("player_before_update", before)
        if before["playing"]:
            cli.error("Pause the player before applying an application update")
    payload = args.installer.read_bytes()
    if sup.stage(0, BUILD, payload)["state"] != 2:
        raise RuntimeError("X rejected the verified installer region")
    emit("staged", {"hash": sha256(payload).hexdigest(), "size": len(payload),
                    "install_or_rollback": args.operation, "target": args.target})
    sup.cmd(0x47)
    result = bytes(sup.usb.ctrl_transfer(0xc0, 0x90, 0, 0, 32, timeout=5000))
    if struct.unpack_from("<I", result)[0] != BUILD:
        raise RuntimeError("Incorrect staged application installer")
    clock = bytes(sup.usb.ctrl_transfer(0xc0, 0x96, 0, 0, 32, timeout=5000))
    ccr, apll, mpll, csr, ddr, ticks, cycles, cause = struct.unpack("<8I", clock)
    emit("clock_before_restart", {key: hex(value) for key, value in
          zip(("ccr", "apll", "mpll", "csr", "ddr", "ost_ticks", "cp0_cycles", "cp0_cause"),
              (ccr, apll, mpll, csr, ddr, ticks, cycles, cause))})
    old_address = sup.usb.address
    try:
        sup.cmd(0x94 if args.operation == "install" else 0x95, TARGETS[args.target], 0x5732)
    except usb.core.USBError as error:
        if error.errno not in (5, 19):
            raise
    cutoff = time.monotonic()+65
    while time.monotonic()<cutoff:
        time.sleep(.3)
        devices = [d for d in usb.core.find(find_all=True,idVendor=0x1209,idProduct=1)
                   if f"{d.bus}-" + ".".join(map(str, d.port_numbers)) == args.port]
        if len(devices)!=1 or (devices[0].address==old_address and sup.version==args.expect):
            continue
        try:
            current = Supervisor(args.port)
            if current.version == args.expect:
                result = player_status(current.usb)
                emit("player_after_restart", {"version": current.version, "address": current.usb.address, **result})
                if result["volume"] or result["playing"]:
                    raise RuntimeError("New application did not boot paused and muted")
                return
        except (ValueError, usb.core.USBError):
            continue
    try:
        tail = bytes(sup.usb.ctrl_transfer(0xc0, 0x90, 0, 0, 32, timeout=2000))
        emit("installer_status", {"words": list(struct.unpack("<8I", tail))})
        sup.cmd(0x74)
    except usb.core.USBError:
        pass
    raise TimeoutError("Expected application did not identify after NAND restart")


if __name__ == "__main__":
    main()
