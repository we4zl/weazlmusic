"""Reconstruct the verified H51 USB updater with the owner's controller patch."""
import argparse
import subprocess
import tempfile
from hashlib import sha256
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("patch", type=Path, help="Factory hw4 V2.2 .upt or its extracted BCM4343A1 .hcd")
parser.add_argument("output", type=Path, help="New file for the USB updater")
args = parser.parse_args()
if args.patch.suffix.lower() == ".upt":
    if sha256(args.patch.read_bytes()).hexdigest() != "5e28f9d56bff9cbb8b4e03a97c9fbe230d88ed570488aab31fdf01d5ade7ae0f":
        parser.error("Expected the original H2 USB-C hw4 V2.2 update")
    with tempfile.TemporaryDirectory() as tmp:
        directory = Path(tmp)
        ubi = directory / "system.ubi"
        with ubi.open("wb") as output:
            subprocess.run(["bsdtar", "-xOf", args.patch, "system.ubi"], stdout=output, check=True)
        subprocess.run(["ubireader_extract_files", "-o", directory / "root", ubi], check=True)
        files = list((directory / "root").rglob("BCM4343A1_001.002.009.0122.0538.hcd"))
        if len(files) != 1:
            parser.error("Cannot identify the hw4 radio patch")
        patch = files[0].read_bytes()
else:
    patch = args.patch.read_bytes()
if sha256(patch).hexdigest() != "9873217acdeaee78bd502a31fa876dc1b120c9d07ca8c16420853bc44ba64972":
    parser.error("Expected the BCM4343A1 patch from the hw4 V2.2 package")
part = Path(__file__).with_name("H51.xpart").read_bytes()
image = part[:94056] + patch + part[94056:]
digest = sha256(image).hexdigest()
if digest != "17275cea3423b951888288e85e557cfc48c6f54745bd1c64e224c4298859c890":
    parser.error("H51 updater checksum mismatch")
with args.output.open("xb") as output:
    output.write(image)
print(digest, args.output)
