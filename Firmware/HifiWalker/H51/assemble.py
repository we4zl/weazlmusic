"""Reconstruct the verified H51 USB updater with the owner's controller patch."""
import argparse
from hashlib import sha256
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("patch", type=Path)
parser.add_argument("output", type=Path, help="New file for the USB updater")
args = parser.parse_args()
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
