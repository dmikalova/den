# initrd

These dracut modules let you unlock the root volume over SSH. The boot stops at
the LUKS passphrase prompt only when the TPM unlock fails, for example on the
first boot after the install.

- `46sshd/` is [dracut-sshd](https://github.com/gsauthof/dracut-sshd), commit
  `2a9b9d8e2e111afd306c91dc651ee4756bf57aa7`, without changes. It is
  GPL-3.0-or-later, not Apache-2.0 like the rest of this repo.
- `45den-net/` adds DHCP and mDNS to the initramfs, so `den.local` resolves at
  the prompt.
- `install.sh` installs both modules and rebuilds every initramfs.

The autoinstall copies this folder and the seed's `authorized_keys` to the
installed system and runs `install.sh`. Kernel updates keep the modules,
because dracut reads them from `/usr/lib/dracut/modules.d` on every rebuild.

## Unlock

```sh
ssh root@den.local
systemd-tty-ask-password-agent
```

The initramfs uses den's normal host keys, so `known_hosts` does not change.
The private host keys are in the initramfs on the unencrypted `/boot`. A
person with the disk can copy them and pretend to be den. They cannot log in
with them.

## Update dracut-sshd

1. Copy the `46sshd/` files from the new upstream commit.
2. Change the commit in this file.
3. Run `install.sh` on den, then reboot and do one SSH unlock.
