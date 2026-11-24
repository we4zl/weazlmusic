# We4zlP0D X · H51

Experimental application update for the **HiFi Walker H2 USB-C hw4 (Ingenic X1000)** with X recovery already installed. The H2's normal X USB updater accepts this build. For a factory H2, follow the first-install section below. Other H2 hardware revisions have different board requirements.

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

## First install on a factory player (experimental)

This Linux kit is for the USB-C **hw4 / erosq_2024** board with the V2.2 factory boot layout. The install stages were exercised on the project H2. The combined first-install command has been checked off-device. A second factory H2 has yet to validate it end to end.

1. Download the hw4 V2.2 factory update from [HiFi Walker](https://hifiwalker.com/pages/download). Install Python 3, PyUSB, libusb, bsdtar and the Python package ubi-reader. With those commands on your PATH, assemble the H51 updater as your normal user:

   ```sh
   python3 assemble.py H2-V2.2.upt usb-installer.bin
   ```

2. Charge the player to at least half, shut it down and unplug headphones. Hold **Menu** while reconnecting USB, then release. The screen can stay black. Find the physical port of the Ingenic ROM (USB ID `a108:1000`) under `/sys/bus/usb/devices`. Set `H2_USB_PORT` to that topology path, for example `3-4.3`. On Linux this lists the matching port paths:

   ```sh
   for d in /sys/bus/usb/devices/*; do
       [ "$(cat "$d/idVendor" 2>/dev/null)" = a108 ] &&
       [ "$(cat "$d/idProduct" 2>/dev/null)" = 1000 ] && basename "$d"
   done
   export H2_USB_PORT=3-4.3  # replace with your player's port
   ```

   The script needs USB read/write permission across re-enumeration.

3. Run the installer with a **new local backup directory on your computer**. Keep USB connected through completion.

   ```sh
   python3 factory_install.py "$H2_USB_PORT" ./my-h2-factory-backup --install ./usb-installer.bin
   ```

   Omit `--install ./usb-installer.bin` for a backup-only pass. To install later, enter ROM mode again and choose a fresh directory. The tool starts recovery in RAM, reads the first 1,536 physical pages (including spare bytes) and compares the entire backup to the qualified factory layout before allowing writes. A different layout stops the install. Keep the backup off the player.

4. The installer writes and verifies the secondary recovery copy before the primary, upgrades recovery to H15, installs H51 in slot A and checks the rebooted player. Successful output includes `player_after_restart` with `version: 51`. After that, ordinary X USB updates use the inactive slot.

If a USB error occurs after writing starts, retain the backup and terminal output. The first-install command expects the original factory layout. Raw factory restoration from an owner's backup has not been hardware-validated.
