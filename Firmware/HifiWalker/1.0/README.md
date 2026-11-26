# We4zlP0d X 1.0 for HiFi Walker H2

This experimental release targets the **USB-C HiFi Walker H2 hw4** with an Ingenic X1000 and the V2.2 factory layout. The player still reports **H64** in Settings and over USB. The name 1.0 identifies the tested H64 build.

We4zlP0d X is ground-up player and board firmware. It reuses no code from the original player firmware or Rockbox. The Bluetooth controller loads separate Broadcom microcode from the owner's factory update. The kit includes the corresponding H64 source, build inputs, and third-party license notices. [Source archive](source-1.0.tar.xz)

## What's in 1.0

- Home and library navigation use partial LCD updates. On the development H2, median USB-input-to-focus time fell from 176 ms to 2.36 ms on Home and from 183 ms to 2.84 ms in Songs.
- Library screens show six rows under Record Shop artwork. Long titles start at the beginning before scrolling.
- Offline playback uses the We4zlMusic card catalog, album artwork, and Bluetooth headphones. Supported local originals include AAC-LC M4A, FLAC, and MP3. The 3.5 mm output is unavailable in this build.
- Flight Recorder retains its bookmark and the common station timeline. Restarting a recording requires confirmation.

## Assemble the tested updater

Use Linux and Python 3. Check the downloaded files with `sha256sum -c SHA256SUMS`. Supply the BCM4343A1 `.hcd` from your own USB-C hw4 V2.2 factory filesystem or the original V2.2 `.upt`. Reading a `.upt` also requires `bsdtar` and `ubireader_extract_files` from the `ubi-reader` package.

```sh
python3 assemble.py /path/to/H2-V2.2.upt usb-installer.bin
```

The assembler verifies the factory input and writes the updater only after the complete 239,596-byte installer matches the tested SHA-256 below.

```text
6d1197958bc5fba1c1d76ab87efd658139a9b72b65980268f72605f751ecf4e6
```

## Install on an H2 already running X

Install PyUSB and libusb. Pause and mute the player, eject its card from Linux, and keep it connected over USB on a charged battery. Set `H2_USB_PORT` to the player's Linux USB topology path and `H2_INACTIVE_SLOT` to the verified inactive application slot (`A` or `B`). Installing into the inactive slot keeps the previous build available for rollback. The updater requires USB read/write access across re-enumeration.

```sh
python3 slot_update.py "$H2_USB_PORT" install "$H2_INACTIVE_SLOT" usb-installer.bin 64
```

The installer verifies the application after writing and waits for H64 to identify after reboot. The development H2 booted H64 from slot A with H63 retained in slot B.

## Start from factory firmware

Use the [experimental factory procedure in the H51 kit](../H51/README.md#first-install-on-a-factory-player-experimental). It backs up and checks the original NAND layout before installing X recovery and H51. Once H51 boots, follow the ordinary X update steps above. Keep the factory backup off the player.

Code from FAAD2 is copyright (c) Nero AG, www.nero.com
