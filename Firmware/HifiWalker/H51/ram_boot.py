"""Load an X supervisor into X1000 on-chip SRAM using the Ingenic USB boot interface."""
from argparse import ArgumentParser
from hashlib import sha256
from pathlib import Path
import usb.core
import usb.util

SRAM_LIMIT = 0x2400

ENTRY = 0xf4001800


def main():
    cli = ArgumentParser(description=__doc__)
    cli.add_argument("port", help="physical USB port, for example 3-4.3")
    args = cli.parse_args()
    path = Path(__file__).with_name("H05.bin")
    image = path.read_bytes()
    expected = "07549bb88f8df74059c4980478ccecc4ca7d5baaf997b2c0d2a0abd7aeba30f7"
    if sha256(image).hexdigest() != expected:
        cli.error("Recovery image checksum mismatch")
    if not 0 < len(image) <= SRAM_LIMIT or len(image) % 4:
        raise SystemExit("Firmware does not fit the SRAM extent")
    matches = [d for d in usb.core.find(find_all=True, idVendor=0xa108, idProduct=0x1000)
               if f"{d.bus}-" + ".".join(map(str, d.port_numbers)) == args.port]
    if len(matches) != 1:
        raise SystemExit("Expected an X1000 in USB boot on the selected port")
    dev = matches[0]
    usb.util.claim_interface(dev, 0)
    try:
        if bytes(dev.ctrl_transfer(0xc0, 0, 0, 0, 8)) != b"X1000_v1":
            raise SystemExit("Unexpected CPU identity")
        def command(op, arg):
            dev.ctrl_transfer(0x40, op, arg >> 16, arg & 65535, b"", timeout=2000)
        # Use the ROM SPL layout. First 2 KiB are the reserved header space.
        data = bytes(2048) + image
        for write in (True, False):
            command(1, ENTRY - 2048)
            command(2, len(data))
            if write:
                if dev.write(1, data, timeout=3000) != len(data):
                    raise SystemExit("Short SRAM write")
            elif bytes(dev.read(0x81, len(data), timeout=3000)) != data:
                raise SystemExit("SRAM readback differs")
        print(f"{args.port} verified {len(image)} firmware bytes in SRAM: {expected}", flush=True)
        command(4, ENTRY)
        print(f"ROM accepted {path.name} start", flush=True)
    finally:
        usb.util.dispose_resources(dev)


if __name__ == "__main__":
    main()
