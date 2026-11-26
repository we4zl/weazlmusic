"""Reconstruct the verified H64 updater using the owner's hw4 controller patch."""

import argparse
from hashlib import sha256
import json
import lzma
from pathlib import Path
import struct
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent


def checked(data, digest, label, size=None):
    if (size is not None and len(data) != size) or sha256(data).hexdigest() != digest:
        raise ValueError(f"Checksum mismatch for {label}")
    return data


def controller_patch(path, manifest):
    controller = manifest["controller"]
    if path.suffix.lower() != ".upt":
        return checked(path.read_bytes(), controller["sha256"], "controller patch", controller["bytes"])

    checked(path.read_bytes(), manifest["factorySha256"], "hw4 V2.2 factory update")
    with tempfile.TemporaryDirectory(prefix="we4zlp0d-factory-") as tmp:
        work = Path(tmp)
        ubi = work/"system.ubi"
        with ubi.open("wb") as image:
            subprocess.run(["bsdtar", "-xOf", str(path), "system.ubi"], stdout=image, check=True)
        subprocess.run(["ubireader_extract_files", "-o", str(work/"root"), str(ubi)],
                       check=True, stdout=subprocess.DEVNULL)
        files = list((work/"root").rglob(controller["name"]))
        if len(files) != 1:
            raise ValueError("Expected one controller patch in the factory update")
        return checked(files[0].read_bytes(), controller["sha256"], "controller patch", controller["bytes"])


def assemble(patch, manifest):
    app = manifest["expanded"]
    public = checked((HERE/"application.xpart.xz").read_bytes(), app["publicSha256"], "public application")
    redacted = lzma.decompress(public)
    offset = manifest["controller"]["offset"]
    expanded = checked(redacted[:offset]+patch+redacted[offset:], app["sha256"],
                       "expanded application", app["bytes"])

    # Match the verified packed player's raw LZMA stream and property header.
    options = dict(id=lzma.FILTER_LZMA1, preset=9, dict_size=1 << 19, lc=1, lp=2, pb=2)
    compressed = lzma.compress(expanded, format=lzma.FORMAT_RAW, filters=[options])
    header = struct.pack("<BI", (options["pb"]*5+options["lp"])*9+options["lc"], options["dict_size"])
    payload = manifest["payload"]
    packed = checked(header+compressed, payload["sha256"], "packed application", payload["bytes"])

    installer = manifest["installer"]
    shell = checked((HERE/"usb-installer.xpart").read_bytes(), installer["publicSha256"], "installer scaffold")
    offset = payload["offset"]
    return checked(shell[:offset]+packed+shell[offset:], installer["sha256"], "complete USB installer",
                   installer["bytes"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("factory", type=Path, help="Original hw4 V2.2 .upt or its extracted .hcd file")
    parser.add_argument("output", type=Path, help="New output path for usb-installer.bin")
    args = parser.parse_args()
    if args.output.exists():
        parser.error("Choose a new output file")
    manifest = json.loads((HERE/"manifest.json").read_text())
    try:
        image = assemble(controller_patch(args.factory, manifest), manifest)
        with args.output.open("xb") as output:
            output.write(image)
    except (OSError, ValueError, lzma.LZMAError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Cannot assemble updater: {error}\n")
    print(sha256(image).hexdigest(), args.output)


if __name__ == "__main__":
    main()
