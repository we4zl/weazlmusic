# We4zlP0D X · H51

Experimental application update for the **HiFi Walker H2 USB-C hw4 (Ingenic X1000)** with X recovery already installed. The H2's normal X USB updater accepts this build. A factory H2 requires a separate first installation of X recovery. Other H2 hardware revisions have different board requirements.

## Assemble the hardware-tested updater

Use Python 3 and the Bluetooth patch from your own hw4 V2.2 factory filesystem. Within that filesystem the patch is at `/lib/firmware/BCM4343A1_001.002.009.0122.0538.hcd`. H51.xpart contains the X portion of the updater. Assembly restores the controller patch at its original offset and checks the complete installer before writing it.

From this directory, run:

```sh
python3 assemble.py /path/to/BCM4343A1_001.002.009.0122.0538.hcd usb-installer.bin
sha256sum usb-installer.bin
```

Expected USB installer SHA-256: `17275cea3423b951888288e85e557cfc48c6f54745bd1c64e224c4298859c890` (239,596 bytes).

## Update an existing X player

On Linux, install PyUSB and libusb. Pause playback, eject any mounted card filesystem and keep the player on USB power. Use its physical USB port path from sysfs and the **inactive** application slot so the previous working build remains available. With those two values set, run:

```sh
python3 slot_update.py "$H2_USB_PORT" install "$H2_INACTIVE_SLOT" usb-installer.bin 51
```

The updater checks the connected player's identity and reports the running build after reboot. In the original hardware test, H51 booted from slot A with H50 retained in slot B. On redock, unchanged music, artwork and radio revision checks each took under 2.5 ms on the test system.
