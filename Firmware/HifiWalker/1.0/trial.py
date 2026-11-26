"""Stage and run bounded H2 X trial packages over the live USB connection."""
import argparse
from hashlib import sha256
import json
from pathlib import Path
import secrets
import struct
import time
import zlib
import usb.core

COOKIE = 0x58324832
SLOTS = (0xF4000000, 0xF4000800)
HEADER = struct.Struct("<4sIIIII")
NAMES = ("state", "active", "build", "result", "previous", "transferred", "ticks", "ddr")


class Supervisor:
    def __init__(self, port):
        candidates = [d for d in usb.core.find(find_all=True, idVendor=0x1209, idProduct=1)
                      if f"{d.bus}-" + ".".join(map(str, d.port_numbers)) == port]
        if len(candidates) != 1:
            raise ValueError("Expected one H2 on the selected port")
        self.usb = candidates[0]
        sysdev = Path("/sys/bus/usb/devices") / port
        if ((sysdev / "product").read_text().strip() != "WeazlP0d X H2"
                or int((sysdev / "devnum").read_text()) != self.usb.address):
            raise ValueError("Expected a live H2 USB attachment")
        revision = (sysdev / "bcdDevice").read_text().strip()
        minor = revision[2:]
        if (len(revision) != 4 or not revision.startswith("01")
                or not minor.isascii() or not minor.isdecimal() or int(minor) < 2):
            raise ValueError("Expected an H2 supervisor revision")
        build = f"H{minor}X".encode()
        nonce = secrets.randbits(32)
        for _ in range(4):
            try:
                identify = bytes(self.usb.ctrl_transfer(0xc0, 0x42, nonce & 65535, nonce >> 16,
                                                        12, timeout=1500))
            except usb.core.USBError:
                continue
            if len(identify) == 12 and identify[:4] == build and identify[4:8] == struct.pack("<I", nonce):
                break
        else:
            raise ValueError("Expected a live H2 X supervisor")
        self.version = int(identify[1:3])
        self.slots = SLOTS if self.version == 2 else (0xa0400000, 0xa0500000)
        self.size = 2048 if self.version == 2 else 0x100000

    def status(self):
        data = bytes(self.usb.ctrl_transfer(0xc0, 0x43, 0, 0, 32, timeout=2500))
        return dict(zip(NAMES, struct.unpack("<8I", data)))

    def cmd(self, command, value=0, index=0):
        self.usb.ctrl_transfer(0x40, command, value, index, timeout=2500)

    def stage(self, slot, build, image, fault=None):
        if not 0 < len(image) <= self.size - HEADER.size or len(image) % 4:
            raise ValueError("Invalid trial size")
        package = HEADER.pack(b"WX2A", 0x20101, self.slots[slot] + HEADER.size,
                              len(image), build, zlib.crc32(image))
        if fault == "wrong_board":
            package = package[:4] + struct.pack("<I", 0x10101) + package[8:]
        if fault == "crc":
            image = bytes([image[0] ^ 1]) + image[1:]
        package += image
        if self.version == 2:
            self.cmd(0x44, len(package), slot)
        else:
            self.cmd(0x44, len(package) & 65535, (slot << 15) | (len(package) >> 16))
        if fault == "short":
            package = package[:-4]
        for (word,) in struct.iter_unpack("<I", package):
            self.cmd(0x45, word & 65535, word >> 16)
        try:
            self.cmd(0x46)
        except usb.core.USBError as error:
            if fault != "short" or error.errno != 32:
                raise
        return self.status()

    def switch(self, request):
        self.cmd(request)
        for _ in range(50):
            result = self.status()
            if result["state"] in (5, 6):
                return result
            time.sleep(.02)
        raise TimeoutError("Candidate has not returned")


def upgrade_to_h03(port):
    supervisor = Supervisor(port)
    status = supervisor.status()
    if supervisor.version != 2 or status["ddr"] != 1 or status["active"] == 1:
        raise ValueError("H03 bridge needs H02, passing DDR, and an inactive slot 1")
    archive = Path(__file__).resolve().parents[3] / "HifiWalker" / "H03"
    entries = {name: digest for digest, name in map(str.split, (archive / "SHA256SUMS").read_text().splitlines())}
    items = {}
    for name in ("H03.bin", "bridge.bin"):
        contents = (archive / name).read_bytes()
        if sha256(contents).hexdigest() != entries[name]:
            raise ValueError(f"{name} checksum mismatch")
        items[name] = contents
    if supervisor.stage(1, 0x4830324c, items["bridge.bin"])["state"] != 2:
        raise ValueError("H02 rejected the bridge")
    supervisor.cmd(0x47)
    dev = supervisor.usb
    identity = bytes(dev.ctrl_transfer(0xc0, 0x60, 0, 0, 4, timeout=2500))
    if identity != b"H02L":
        raise ValueError("Expected the H02 bridge")
    try:
        image = items["H03.bin"]
        supervisor.cmd(0x61, len(image), 0)
        for (word,) in struct.iter_unpack("<I", image):
            supervisor.cmd(0x62, word & 65535, word >> 16)
        crc = zlib.crc32(image)
        supervisor.cmd(0x63, crc & 65535, crc >> 16)
        if bytes(dev.ctrl_transfer(0xc0, 0x64, 0, 0, 4, timeout=2500)) != b"\x01\x00\x00\x00":
            raise ValueError("H03 DRAM readback did not verify")
    except Exception:
        supervisor.cmd(0x66)
        raise
    try:
        supervisor.cmd(0x65)
    except usb.core.USBError as error:
        # Starting the new firmware resets endpoint zero. The kernel can
        # return EIO or ENODEV while retiring the original attachment.
        if error.errno not in (5, 19):
            raise
    for _ in range(50):
        time.sleep(.2)
        try:
            running = Supervisor(port)
            if running.version == 3:
                return running.status()
        except (ValueError, usb.core.USBError):
            continue
    raise TimeoutError("H03 did not identify after reconnect")


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("port")
    commands = cli.add_subparsers(dest="action", required=True)
    for command in ("status", "activate", "rollback", "ddr", "upgrade-h03"):
        commands.add_parser(command)
    stage = commands.add_parser("stage")
    stage.add_argument("slot", type=int, choices=(0, 1))
    stage.add_argument("build", type=lambda n: int(n, 0))
    stage.add_argument("image", type=Path)
    args = cli.parse_args()
    if args.action == "upgrade-h03":
        print(json.dumps(upgrade_to_h03(args.port), indent=2))
        return
    supervisor = Supervisor(args.port)
    if args.action == "stage":
        result = supervisor.stage(args.slot, args.build, args.image.read_bytes())
        if result["state"] != 2:
            raise ValueError(f"Stage failed: {result}")
    elif args.action == "ddr":
        status = supervisor.status()
        if not status["ddr"]:
            supervisor.cmd(0x49)
        for _ in range(50):
            result = supervisor.status()
            if result["ddr"]:
                break
            time.sleep(.04)
    elif args.action != "status":
        result = supervisor.switch({"activate": 0x47, "rollback": 0x48}[args.action])
    else:
        result = supervisor.status()
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
