# Memory test

`den` runs without ECC memory. A bad bit is silent: there is no machine check,
no EDAC counter, and no log entry. The only time you can see a memory fault is
before the box holds data.

Test the memory after you set the EXPO profile and before you install.

## Build the stick

```bash
den usb memtest
```

`den usb memtest` downloads the image, checks it against the published
checksum list, and unpacks it. It then lists the external disks and asks
which one to write. Pass `--version` for a different release.

The memtest86+ project publishes no signature over its checksum list. The
checksum proves the download is intact. It does not prove the host is honest.
This is weaker than the Ubuntu ISO, which `den usb build` verifies against
Canonical's signing key.

Use a second USB stick. The install image has to survive this step.

## Boot it

Select the stick from the boot menu. The test starts on its own and repeats
until you stop it. Press `Esc` to quit and `c` for the configuration menu.

Check that CPU sequencing is set to parallel. A sequential run over 64 GB
takes hours longer for no extra coverage.

Watch the first few percent, then extrapolate to get a time estimate.

## Secure Boot

memtest86+ is not signed for Secure Boot. Turn Secure Boot off to run it.

Toggling Secure Boot changes PCR 7, and the LUKS key is sealed to PCR 7. Set
Secure Boot to its final value before the installer enrolls the TPM. If you
change it afterwards, the TPM refuses to release the key and every boot asks
for the passphrase until you enroll again.

## Read the result

One error is a failure. Set the DRAM profile down to DDR5-5200 and test again.
5200 is AMD's rated speed for two dual-rank modules on this CPU, so the fall
back is to the supported operating point, not below it.

A clean pass is not the whole story. memtest86+ walks the address space, but
it does not hold the machine at load for hours. Run a sustained test under
Linux during burn-in as well:

```bash
sudo apt install stress-ng
stress-ng --vm 16 --vm-bytes 80% --vm-method all --verify \
          --timeout 8h --metrics --log-file ~/stress-ng.log
```

`--verify` writes known patterns and reads them back. Without it the run only
makes heat. `stress-ng` cannot test the memory the kernel itself occupies,
which is why it follows memtest86+ rather than replacing it.
