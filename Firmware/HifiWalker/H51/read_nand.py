"""Read selected H2 SPI NAND pages through the read-only X board diagnostic."""
import argparse
from hashlib import sha256
import json
import struct
from pathlib import Path
from trial import Supervisor

BUILD = 0x4830334e
PAGE = 2048
SPARE = 64


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("port")
    cli.add_argument("out", type=Path, help="new working directory for these readbacks")
    cli.add_argument("--start", type=int, default=0)
    cli.add_argument("--count", type=int, default=8)
    cli.add_argument("--image", type=Path, help="Read-only diagnostic binary")
    cli.add_argument("--sd-check", action="store_true", help="Read the SD MBR and partition boot sector")
    args = cli.parse_args()
    if args.count <= 0 or args.start < 0 or args.start + args.count > 65536:
        cli.error("Page range outside the 128 MiB NAND geometry")
    payload = (args.image or Path(__file__).with_name("nand-reader.bin")).read_bytes()
    sup = Supervisor(args.port)
    if sup.version != 5 or sup.status()["active"] == 0:
        cli.error("Read diagnostic requires RAM H05 and vacant trial slot 0")
    if sup.stage(0, BUILD, payload)["state"] != 2:
        raise ValueError("Supervisor rejected the read-only diagnostic")
    args.out.mkdir(parents=True, exist_ok=False)

    def read(op, size, offset=0):
        return bytes(sup.usb.ctrl_transfer(0xc0, op, offset, 0, size, timeout=2000))

    sup.cmd(0x47)
    try:
        primary, alternate, ecc, protection, result = struct.unpack("<5I", read(0x70, 20))
        info = dict(id=primary.to_bytes(4,"little").hex(), alternate=alternate.to_bytes(4,"little").hex(),
                    config=ecc, protection=protection, result=result)
        print(json.dumps(info), flush=True)
        (args.out / "chip.json").write_text(json.dumps(info, indent=2) + "\n")
        if result:
            return 1
        with (args.out / "pages.bin").open("xb") as pages, (args.out / "page-status.jsonl").open("x") as status:
            for page in range(args.start, args.start + args.count):
                sup.cmd(0x71, page)
                rc, flags = struct.unpack("<2I", read(0x73, 8))
                if rc or flags & 0x30 == 0x20:
                    raise OSError(f"Read page {page} failed with code {rc} status {flags:02x}")
                data = b"".join(read(0x72, min(PAGE+SPARE-offset, 64), offset)
                                for offset in range(0, PAGE+SPARE, 64))
                if len(data) != PAGE+SPARE:
                    raise OSError(f"Short read on page {page}")
                pages.write(data)
                status.write(json.dumps(dict(page=page, flags=flags, spare_prefix=data[PAGE:PAGE+4].hex(),
                                             sha256=sha256(data).hexdigest())) + "\n")
        if args.sd_check:
            sup.cmd(0x75)
            rc, sectors = struct.unpack("<2I", read(0x76, 8))
            sd = {"open_result": rc, "sectors": sectors}
            if rc == 0:
                def sector(lba):
                    sup.cmd(0x77, lba & 65535, lba >> 16)
                    rc, details = struct.unpack("<2I", read(0x73, 8))
                    if rc:
                        raise OSError(f"SD read failed at sector {lba}: {rc} (controller {details:08x})")
                    return b"".join(read(0x72, 64, i) for i in range(0, 512, 64))
                mbr = sector(0)
                (args.out / "sd-mbr.bin").write_bytes(mbr)
                if mbr[510:512] == b"\x55\xaa" and mbr[450] in (0x0b, 0x0c):
                    part, count = struct.unpack_from("<II", mbr, 454)
                    if part and part + count <= sectors:
                        boot = sector(part)
                        (args.out / "sd-volume.bin").write_bytes(boot)
                        sd.update(partition_lba=part, partition_sectors=count,
                                  fat32_signature=boot[82:90].decode("ascii", errors="replace"),
                                  fat_uuid=f"{int.from_bytes(boot[67:71], 'little'):08X}")
            (args.out / "sd.json").write_text(json.dumps(sd, indent=2) + "\n")
            print(json.dumps(sd), flush=True)
    finally:
        sup.cmd(0x74)
    print(json.dumps(sup.status()), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
